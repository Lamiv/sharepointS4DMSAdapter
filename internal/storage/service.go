// Package storage is the interface-neutral document service shared by the
// Document REST API and the SAP Content Repository interface. It maps
// logical repositories to SharePoint libraries/folders, enforces that every
// item accessed lies inside its repository root, and caches item metadata.
package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Lamiv/zaks-drive/internal/apperr"
	"github.com/Lamiv/zaks-drive/internal/config"
	"github.com/Lamiv/zaks-drive/internal/graph"
	"github.com/Lamiv/zaks-drive/internal/transfer"
)

var (
	ErrNotFound = errors.New("not found")
	ErrReadOnly = errors.New("repository is read-only")
	ErrInvalid  = errors.New("invalid request")
	// ErrReserved: the path belongs to another interface (the SAP content
	// server) and is not reachable through this repository handle.
	ErrReserved = errors.New("path is reserved for the SAP content server")
)

type Repository struct {
	ID       string
	DriveID  string
	RootPath string // drive-relative, no leading/trailing slash; "" = drive root
	ReadOnly bool
	// WebURL is the browser URL of the library, for logs and diagnostics.
	WebURL string
	// reserved holds repository-relative folders owned by the SAP content
	// server. They are invisible to, and immutable through, this handle.
	reserved []string
}

// Unrestricted returns a handle on the same library without reserved-path
// restrictions, for the interface that owns those paths.
func (r *Repository) Unrestricted() *Repository {
	cp := *r
	cp.reserved = nil
	return &cp
}

// hidden reports whether rel is a reserved folder or lies inside one.
func (r *Repository) hidden(rel string) bool {
	for _, p := range r.reserved {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// containsReserved reports whether deleting folder rel would also delete a
// reserved folder.
func (r *Repository) containsReserved(rel string) bool {
	for _, p := range r.reserved {
		if rel == "" || strings.HasPrefix(p, rel+"/") {
			return true
		}
	}
	return false
}

// visible reports whether a drive item is inside the repository and not in
// a reserved folder.
func (r *Repository) visible(it *graph.DriveItem) bool {
	rel, ok := r.Rel(it.Path())
	return ok && !r.hidden(rel)
}

// abs converts a repository-relative path to a drive-relative path.
func (r *Repository) abs(rel string) (string, error) {
	clean, err := CleanPath(rel)
	if err != nil {
		return "", err
	}
	return strings.Trim(r.RootPath+"/"+clean, "/"), nil
}

// Rel converts a drive path ("/root/sub/x.pdf") to a repository-relative one;
// ok is false when the item is outside the repository.
func (r *Repository) Rel(drivePath string) (string, bool) {
	p := strings.Trim(drivePath, "/")
	if r.RootPath == "" {
		return p, true
	}
	if p == r.RootPath {
		return "", true
	}
	if strings.HasPrefix(p, r.RootPath+"/") {
		return p[len(r.RootPath)+1:], true
	}
	return "", false
}

// CleanPath validates a relative path: no "..", no empty or dot segments,
// no characters SharePoint rejects.
func CleanPath(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" {
		return "", nil
	}
	for _, seg := range strings.Split(p, "/") {
		if err := ValidName(seg); err != nil {
			return "", err
		}
	}
	return path.Clean(p), nil
}

func ValidName(name string) error {
	const bad = "\"*:<>?/\\|\x00"
	switch {
	case name == "" || name == "." || name == "..":
		return apperr.New(apperr.NameInvalid, "the name %q is not allowed (empty, \".\" and \"..\" are reserved)", name).Wrap(ErrInvalid)
	case len(name) > 255:
		return apperr.New(apperr.NameTooLong, "the name is %d characters long; the limit is 255 (%.40q...)", len(name), name).Wrap(ErrInvalid)
	case strings.ContainsAny(name, bad):
		var found []string
		for _, r := range name {
			if strings.ContainsRune(bad, r) && !strings.Contains(strings.Join(found, ""), string(r)) {
				found = append(found, string(r))
			}
		}
		return apperr.New(apperr.NameInvalid, "the name %q contains characters SharePoint does not allow: %s", name, strings.Join(found, " ")).Wrap(ErrInvalid)
	case strings.HasSuffix(name, "."):
		return apperr.New(apperr.NameInvalid, "the name %q ends with a dot, which SharePoint does not allow", name).Wrap(ErrInvalid)
	case strings.HasPrefix(name, " ") || strings.HasSuffix(name, " "):
		return apperr.New(apperr.NameInvalid, "the name %q starts or ends with a space, which SharePoint does not allow", name).Wrap(ErrInvalid)
	}
	return nil
}

// maxSharePointPath is SharePoint's limit for the decoded path after the
// domain, including site, library and all folders.
const maxSharePointPath = 400

// checkPathLength rejects a path SharePoint would refuse, with a clear
// message, before any bytes are sent. rel is relative to the repository root.
func (r *Repository) checkPathLength(rel string) error {
	prefix := 40 // typical /sites/<site>/<library> when the URL is unknown
	if u, err := url.Parse(r.WebURL); err == nil && u.Path != "" {
		prefix = len(u.Path)
	}
	total := prefix + 1 + len(strings.Trim(r.RootPath+"/"+rel, "/"))
	if total > maxSharePointPath {
		return apperr.New(apperr.PathTooLong, "the path would be about %d characters; SharePoint allows %d (library path %d + root folder %q + %q)",
			total, maxSharePointPath, prefix, r.RootPath, rel).Wrap(ErrInvalid)
	}
	return nil
}

// notFound builds the coded "not found" error.
func notFound(format string, args ...any) error {
	return apperr.New(apperr.DocNotFound, format, args...).Wrap(ErrNotFound)
}

func readOnly(r *Repository) error {
	return apperr.New(apperr.RepoReadOnly, "repository %q is read-only (readOnly: true in config.yaml)", r.ID).Wrap(ErrReadOnly)
}

func reserved(r *Repository, rel string) error {
	return apperr.New(apperr.PathReserved, "%q is inside the SAP content server folder of repository %q", rel, r.ID).Wrap(ErrReserved)
}

type Service struct {
	g         *graph.Client
	eng       *transfer.Engine
	repos     map[string]*Repository
	order     []string
	cache     *itemCache
	log       *slog.Logger
	cursorKey []byte
}

func NewService(ctx context.Context, g *graph.Client, eng *transfer.Engine, cfgs []config.RepositoryConfig, cacheTTL time.Duration, log *slog.Logger) (*Service, error) {
	s := &Service{g: g, eng: eng, repos: map[string]*Repository{}, cache: newItemCache(cacheTTL, 20000), log: log}
	s.cursorKey = make([]byte, 32)
	_, _ = rand.Read(s.cursorKey)
	for _, rc := range cfgs {
		drive := rc.DriveID
		if drive == "" {
			var err error
			if drive, err = g.ResolveDrive(ctx, rc.UserID, rc.SiteID, rc.SiteURL, rc.DriveName); err != nil {
				return nil, fmt.Errorf("repository %s: resolve drive: %w", rc.ID, err)
			}
			log.Info("resolved repository drive", "repository", rc.ID, "drive_id", drive)
		}
		repo := &Repository{ID: rc.ID, DriveID: drive, RootPath: rc.RootPath, ReadOnly: rc.ReadOnly}
		if _, web, err := g.DriveInfo(ctx, drive); err == nil {
			repo.WebURL = web
		}
		log.Info("repository ready: documents are stored here", "repository", rc.ID, "library_url", repo.WebURL, "root_path", rc.RootPath)
		s.repos[rc.ID] = repo
		s.order = append(s.order, rc.ID)
	}
	return s, nil
}

func (s *Service) Engine() *transfer.Engine { return s.eng }

// SetCursorKey sets the key that authenticates paging cursors. Instances
// behind one load balancer must share it; the default is random per process.
func (s *Service) SetCursorKey(key []byte) { s.cursorKey = key }

// ReservePath hides folder rel of repository id from the shared handle
// (used by the REST API). Call before serving requests.
func (s *Service) ReservePath(id, rel string) error {
	r, err := s.Repository(id)
	if err != nil {
		return err
	}
	clean, err := CleanPath(rel)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("%w: cannot reserve the repository root", ErrInvalid)
	}
	r.reserved = append(r.reserved, clean)
	return nil
}

func (s *Service) Repository(id string) (*Repository, error) {
	if r, ok := s.repos[id]; ok {
		return r, nil
	}
	return nil, apperr.New(apperr.RepoNotFound, "repository %q does not exist or this credential may not use it", id).Wrap(ErrNotFound)
}

func (s *Service) Repositories() []*Repository {
	out := make([]*Repository, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.repos[id])
	}
	return out
}

// mapErr converts Graph "not found" into ErrNotFound for callers.
func mapErr(err error) error {
	if graph.IsNotFound(err) {
		return apperr.New(apperr.DocNotFound, "SharePoint reports that the item or folder does not exist (%v)", err).Wrap(ErrNotFound)
	}
	return err
}

// Get returns item metadata (including a fresh-enough download URL) for an
// item inside repo. Results are cached briefly.
func (s *Service) Get(ctx context.Context, repo *Repository, id string) (*graph.DriveItem, error) {
	if it := s.cache.get(repo.DriveID, id); it != nil {
		if repo.visible(it) {
			return it, nil
		}
		return nil, notFound("item %s is not inside repository %q", id, repo.ID)
	}
	it, err := s.g.GetItem(ctx, repo.DriveID, id)
	if err != nil {
		return nil, mapErr(err)
	}
	if !repo.visible(it) {
		return nil, notFound("item %s exists in the library but is not inside repository %q (root folder %q)", id, repo.ID, repo.RootPath)
	}
	s.cache.put(repo.DriveID, it)
	return it, nil
}

// Refresh bypasses the cache.
func (s *Service) Refresh(ctx context.Context, repo *Repository, id string) (*graph.DriveItem, error) {
	s.cache.drop(repo.DriveID, id)
	return s.Get(ctx, repo, id)
}

func (s *Service) GetByPath(ctx context.Context, repo *Repository, rel string) (*graph.DriveItem, error) {
	p, err := repo.abs(rel)
	if err != nil {
		return nil, err
	}
	if clean, _ := CleanPath(rel); repo.hidden(clean) {
		return nil, notFound("%q does not exist in repository %q", rel, repo.ID)
	}
	it, err := s.g.GetItemByPath(ctx, repo.DriveID, p)
	if err != nil {
		return nil, mapErr(err)
	}
	s.cache.put(repo.DriveID, it)
	return it, nil
}

type Page struct {
	Items  []graph.DriveItem
	Cursor string
}

// Cursors wrap Graph's @odata.nextLink. They are authenticated with an HMAC
// bound to the repository, drive and listing scope (folder or query), so a
// caller cannot substitute a link to another drive, site or folder.

func (s *Service) cursorMAC(repo *Repository, scope, link string) []byte {
	m := hmac.New(sha256.New, s.cursorKey)
	for _, part := range []string{repo.ID, repo.DriveID, scope, link} {
		m.Write([]byte(part))
		m.Write([]byte{0})
	}
	return m.Sum(nil)[:16]
}

func (s *Service) encodeCursor(repo *Repository, scope, link string) string {
	if link == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(link)) + "." + base64.RawURLEncoding.EncodeToString(s.cursorMAC(repo, scope, link))
}

func (s *Service) decodeCursor(repo *Repository, scope, c string) (string, error) {
	if c == "" {
		return "", nil
	}
	bad := apperr.New(apperr.CursorInvalid, "the cursor was not issued by this server for this listing").Wrap(ErrInvalid)
	linkPart, macPart, ok := strings.Cut(c, ".")
	if !ok {
		return "", bad
	}
	link, err1 := base64.RawURLEncoding.DecodeString(linkPart)
	mac, err2 := base64.RawURLEncoding.DecodeString(macPart)
	if err1 != nil || err2 != nil || !hmac.Equal(mac, s.cursorMAC(repo, scope, string(link))) {
		return "", bad
	}
	return string(link), nil
}

// visibleItems keeps only items inside the repository and outside reserved
// folders (defence in depth for listings and search results).
func visibleItems(repo *Repository, items []graph.DriveItem) []graph.DriveItem {
	out := items[:0]
	for _, it := range items {
		if it.ParentReference != nil && it.ParentReference.Path != "" && !repo.visible(&it) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (s *Service) List(ctx context.Context, repo *Repository, rel string, top int, cursor string) (*Page, error) {
	p, err := repo.abs(rel)
	if err != nil {
		return nil, err
	}
	if clean, _ := CleanPath(rel); repo.hidden(clean) {
		return nil, notFound("no such item in repository %q", repo.ID)
	}
	scope := "list:" + p
	next, err := s.decodeCursor(repo, scope, cursor)
	if err != nil {
		return nil, err
	}
	pg, err := s.g.ListChildren(ctx, repo.DriveID, p, top, next)
	if err != nil {
		return nil, mapErr(err)
	}
	return &Page{Items: visibleItems(repo, pg.Items), Cursor: s.encodeCursor(repo, scope, pg.NextLink)}, nil
}

func (s *Service) Search(ctx context.Context, repo *Repository, query string, top int, cursor string) (*Page, error) {
	if strings.TrimSpace(query) == "" {
		return nil, apperr.New(apperr.QueryEmpty, "the search text `q` is empty").Wrap(ErrInvalid)
	}
	scope := "search:" + query
	next, err := s.decodeCursor(repo, scope, cursor)
	if err != nil {
		return nil, err
	}
	pg, err := s.g.Search(ctx, repo.DriveID, repo.RootPath, query, top, next)
	if err != nil {
		return nil, mapErr(err)
	}
	// Defence in depth: drop hits outside the repository (in case the
	// service widens a folder-scoped search) and inside reserved folders.
	return &Page{Items: visibleItems(repo, pg.Items), Cursor: s.encodeCursor(repo, scope, pg.NextLink)}, nil
}

// UploadRequest creates a new document in folder (repository-relative).
type UploadRequest struct {
	Folder   string
	Name     string
	Conflict graph.ConflictBehavior
	Body     io.Reader
	Size     int64 // -1 if unknown
	// IfMatch makes the write conditional on the current eTag of an
	// existing file (412 otherwise). Small bodies only.
	IfMatch string
}

func (s *Service) Upload(ctx context.Context, repo *Repository, req UploadRequest) (*graph.DriveItem, error) {
	if repo.ReadOnly {
		return nil, readOnly(repo)
	}
	if err := ValidName(req.Name); err != nil {
		return nil, err
	}
	folder, err := repo.abs(req.Folder)
	if err != nil {
		return nil, err
	}
	if clean, _ := CleanPath(req.Folder); repo.hidden(strings.TrimLeft(clean+"/"+req.Name, "/")) {
		return nil, reserved(repo, strings.TrimLeft(clean+"/"+req.Name, "/"))
	}
	if err := repo.checkPathLength(strings.Trim(req.Folder, "/") + "/" + req.Name); err != nil {
		return nil, err
	}
	if req.Conflict == "" {
		req.Conflict = graph.ConflictRename
	}
	t := transfer.Target{Drive: repo.DriveID, Path: strings.TrimLeft(folder+"/"+req.Name, "/"), Conflict: req.Conflict, IfMatch: req.IfMatch}
	it, err := s.eng.Upload(ctx, t, req.Body, req.Size)
	if err != nil {
		return nil, mapErr(err)
	}
	s.cache.drop(repo.DriveID, it.ID)
	return it, nil
}

// Replace uploads new content for an existing item (new SharePoint version).
func (s *Service) Replace(ctx context.Context, repo *Repository, id, ifMatch string, body io.Reader, size int64) (*graph.DriveItem, error) {
	if repo.ReadOnly {
		return nil, readOnly(repo)
	}
	cur, err := s.Get(ctx, repo, id)
	if err != nil {
		return nil, err
	}
	if cur.Folder != nil {
		return nil, apperr.New(apperr.ItemIsFolder, "item %s is a folder", id).Wrap(ErrInvalid)
	}
	s.cache.drop(repo.DriveID, id)
	it, err := s.eng.Upload(ctx, transfer.Target{Drive: repo.DriveID, ItemID: id, IfMatch: ifMatch}, body, size)
	if err != nil {
		return nil, mapErr(err)
	}
	return it, nil
}

func (s *Service) Delete(ctx context.Context, repo *Repository, id, ifMatch string) error {
	if repo.ReadOnly {
		return readOnly(repo)
	}
	it, err := s.Get(ctx, repo, id)
	if err != nil {
		return err
	}
	if rel, _ := repo.Rel(it.Path()); it.Folder != nil && repo.containsReserved(rel) {
		return reserved(repo, rel)
	}
	s.cache.drop(repo.DriveID, id)
	return mapErr(s.g.Delete(ctx, repo.DriveID, id, ifMatch))
}

func (s *Service) CreateFolder(ctx context.Context, repo *Repository, rel string) (*graph.DriveItem, error) {
	if repo.ReadOnly {
		return nil, readOnly(repo)
	}
	p, err := repo.abs(rel)
	if err != nil {
		return nil, err
	}
	if p == "" {
		return nil, apperr.New(apperr.FolderPathRequired, "the folder path is empty").Wrap(ErrInvalid)
	}
	if clean, _ := CleanPath(rel); repo.hidden(clean) {
		return nil, reserved(repo, rel)
	}
	it, err := s.g.CreateFolderPath(ctx, repo.DriveID, p)
	return it, mapErr(err)
}

// EnsureFolder makes sure folder rel exists, creating parents as needed.
// Only the last segment is created with a single call when the parent is
// known to exist (parentKnown), which keeps hot paths to one Graph request.
func (s *Service) EnsureFolder(ctx context.Context, repo *Repository, rel string, parentKnown bool) error {
	if repo.ReadOnly {
		return readOnly(repo)
	}
	p, err := repo.abs(rel)
	if err != nil {
		return err
	}
	if !parentKnown {
		_, err := s.g.CreateFolderPath(ctx, repo.DriveID, p)
		return mapErr(err)
	}
	parent, name := path.Split(p)
	return mapErr(s.g.EnsureChildFolder(ctx, repo.DriveID, strings.TrimSuffix(parent, "/"), name))
}

func (s *Service) Versions(ctx context.Context, repo *Repository, id string) ([]graph.Version, error) {
	if _, err := s.Get(ctx, repo, id); err != nil {
		return nil, err
	}
	v, err := s.g.Versions(ctx, repo.DriveID, id)
	return v, mapErr(err)
}

// Download streams content to w. The caller sets descriptive headers first.
func (s *Service) Download(ctx context.Context, w http.ResponseWriter, repo *Repository, it *graph.DriveItem, rangeHdr string) (transfer.DownloadResult, error) {
	refresh := func() (string, error) {
		fresh, err := s.Refresh(ctx, repo, it.ID)
		if err != nil {
			return "", err
		}
		return fresh.DownloadURL, nil
	}
	url := it.DownloadURL
	if url == "" {
		var err error
		if url, err = refresh(); err != nil {
			return transfer.DownloadResult{}, err
		}
	}
	if url == "" {
		// No pre-authenticated URL available: stream through /content.
		resp, err := s.g.OpenContent(ctx, repo.DriveID, it.ID, rangeHdr)
		if err != nil {
			return transfer.DownloadResult{}, mapErr(err)
		}
		res, err := s.eng.StreamOpened(ctx, w, resp)
		return res, mapErr(err)
	}
	res, err := s.eng.Stream(ctx, w, url, rangeHdr, refresh)
	return res, mapErr(err)
}

// Open returns the raw content response (optionally a byte range) for it,
// refreshing an expired download URL once. The caller closes the body.
func (s *Service) Open(ctx context.Context, repo *Repository, it *graph.DriveItem, rangeHdr string) (*http.Response, error) {
	refresh := func() (string, error) {
		fresh, err := s.Refresh(ctx, repo, it.ID)
		if err != nil {
			return "", err
		}
		return fresh.DownloadURL, nil
	}
	url := it.DownloadURL
	if url == "" {
		var err error
		if url, err = refresh(); err != nil {
			return nil, err
		}
	}
	if url == "" {
		resp, err := s.g.OpenContent(ctx, repo.DriveID, it.ID, rangeHdr)
		return resp, mapErr(err)
	}
	resp, err := s.eng.Open(ctx, url, rangeHdr, refresh)
	return resp, mapErr(err)
}

// ListAll returns every child of a repository-relative folder.
func (s *Service) ListAll(ctx context.Context, repo *Repository, rel string) ([]graph.DriveItem, error) {
	var all []graph.DriveItem
	cursor := ""
	for {
		pg, err := s.List(ctx, repo, rel, 1000, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, pg.Items...)
		if pg.Cursor == "" {
			return all, nil
		}
		cursor = pg.Cursor
	}
}

func (s *Service) Thumbnail(ctx context.Context, repo *Repository, id, size string) (*http.Response, error) {
	if _, err := s.Get(ctx, repo, id); err != nil {
		return nil, err
	}
	resp, err := s.g.OpenThumbnail(ctx, repo.DriveID, id, size)
	return resp, mapErr(err)
}

// Ping checks Graph connectivity for the first repository.
func (s *Service) Ping(ctx context.Context) error {
	for _, id := range s.order {
		return s.g.Ping(ctx, s.repos[id].DriveID)
	}
	return nil
}

// ---- small TTL cache for item metadata ----

type cacheEntry struct {
	item    *graph.DriveItem
	expires time.Time
}

type itemCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]cacheEntry
}

func newItemCache(ttl time.Duration, max int) *itemCache {
	return &itemCache{ttl: ttl, max: max, m: map[string]cacheEntry{}}
}

func (c *itemCache) get(drive, id string) *graph.DriveItem {
	if c.ttl <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[drive+"|"+id]
	if !ok {
		return nil
	}
	if time.Now().After(e.expires) {
		delete(c.m, drive+"|"+id)
		return nil
	}
	return e.item
}

func (c *itemCache) put(drive string, it *graph.DriveItem) {
	if c.ttl <= 0 || it == nil || it.ID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		// Evict arbitrary entries (map order is random) — cheap and adequate
		// for a short-TTL cache.
		n := c.max / 10
		for k := range c.m {
			delete(c.m, k)
			if n--; n <= 0 {
				break
			}
		}
	}
	c.m[drive+"|"+it.ID] = cacheEntry{item: it, expires: time.Now().Add(c.ttl)}
}

func (c *itemCache) drop(drive, id string) {
	c.mu.Lock()
	delete(c.m, drive+"|"+id)
	c.mu.Unlock()
}
