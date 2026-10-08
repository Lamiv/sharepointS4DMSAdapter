package contentrepo

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"sharepointadapter/internal/config"
	"sharepointadapter/internal/graph"
	"sharepointadapter/internal/storage"
)

// Storage layout in SharePoint, below the repository root:
//
//	<folder>/<shard>/<docId>/<compId>      one file per component
//	<folder>/<shard>/<docId>/~sapdoc.json  sidecar: content types, docProt, timestamps
//	<folder>/~certs/<hash>.json            certificates received via putCert
//
// shard is the first byte (hex) of SHA-1(docId); it spreads documents over
// 256 folders so no SharePoint folder grows unbounded.

const sidecarName = "~sapdoc.json"

var (
	errDocExists  = errors.New("document already exists")
	errCompExists = errors.New("component already exists")
	errBusy       = errors.New("concurrent modification; retry")
)

type contentRep struct {
	id     string
	cfg    config.ContentRepositoryConfig
	repo   *storage.Repository
	folder string // repository-relative

	shardsMu sync.Mutex
	shards   map[string]bool // shard folders known to exist
	baseOK   bool
}

type compMeta struct {
	ContentType string    `json:"contentType,omitempty"`
	Charset     string    `json:"charset,omitempty"`
	Version     string    `json:"version,omitempty"`
	Created     time.Time `json:"created"`
	Modified    time.Time `json:"modified"`
}

type docMeta struct {
	DocID      string               `json:"docId"`
	ContRep    string               `json:"contRep"`
	DocProt    string               `json:"docProt,omitempty"`
	Created    time.Time            `json:"created"`
	Modified   time.Time            `json:"modified"`
	Components map[string]*compMeta `json:"components"`
}

type component struct {
	id   string
	item graph.DriveItem
	meta compMeta
}

func (c *component) contentType() string {
	if c.meta.ContentType != "" {
		return c.meta.ContentType
	}
	if c.item.File != nil && c.item.File.MimeType != "" {
		return c.item.File.MimeType
	}
	return "application/octet-stream"
}

type document struct {
	id      string
	meta    *docMeta
	sidecar *graph.DriveItem
	comps   []*component // sorted by compId
}

func (d *document) comp(id string) *component {
	for _, c := range d.comps {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (d *document) created() time.Time {
	if !d.meta.Created.IsZero() {
		return d.meta.Created
	}
	var t time.Time
	for _, c := range d.comps {
		if t.IsZero() || c.meta.Created.Before(t) {
			t = c.meta.Created
		}
	}
	return t
}

func (d *document) modified() time.Time {
	t := d.meta.Modified
	for _, c := range d.comps {
		if c.meta.Modified.After(t) {
			t = c.meta.Modified
		}
	}
	return t
}

// ---- name encoding ----

// encodeName maps an SAP docId/compId to a SharePoint-safe file name,
// reversibly. Characters SharePoint rejects (and '%', '~', '#') become %XX;
// so do a leading/trailing space and a trailing dot. A leading '~' is always
// escaped, which keeps "~sapdoc.json" and "~certs" out of the SAP namespace.
func encodeName(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty identifier", storage.ErrInvalid)
	}
	lower := strings.ToLower(s)
	escUnderscore := strings.Contains(lower, "_vti_")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		esc := c < 0x20 || c == 0x7f || strings.IndexByte("\"*:<>?/\\|%~#", c) >= 0 ||
			(c == ' ' && (i == 0 || i == len(s)-1)) ||
			(c == '.' && i == len(s)-1) ||
			(c == '_' && escUnderscore)
		if esc {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	if b.Len() > 255 {
		return "", fmt.Errorf("%w: identifier too long", storage.ErrInvalid)
	}
	return b.String(), nil
}

func decodeName(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				b.WriteByte(v[0])
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func shardOf(docID string) string {
	sum := sha1.Sum([]byte(docID))
	return hex.EncodeToString(sum[:1])
}

func (cr *contentRep) docFolder(docID string) (string, error) {
	enc, err := encodeName(docID)
	if err != nil {
		return "", err
	}
	return cr.folder + "/" + shardOf(docID) + "/" + enc, nil
}

// ---- document cache ----

type docCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]docCacheEntry
}

type docCacheEntry struct {
	doc     *document
	expires time.Time
}

func (c *docCache) get(key string) *document {
	if c.ttl <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.m, key)
		return nil
	}
	return e.doc
}

func (c *docCache) put(key string, d *document) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 20000 {
		for k := range c.m {
			delete(c.m, k)
			if len(c.m) < 18000 {
				break
			}
		}
	}
	c.m[key] = docCacheEntry{doc: d, expires: time.Now().Add(c.ttl)}
}

func (c *docCache) drop(key string) {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}

// ---- document store ----

type docStore struct {
	svc   *storage.Service
	cache *docCache
}

func cacheKey(cr *contentRep, docID string) string { return cr.id + "|" + docID }

// load reads a document: one folder listing plus the sidecar.
func (s *docStore) load(ctx context.Context, cr *contentRep, docID string) (*document, error) {
	key := cacheKey(cr, docID)
	if d := s.cache.get(key); d != nil {
		return d, nil
	}
	folder, err := cr.docFolder(docID)
	if err != nil {
		return nil, err
	}
	items, err := s.svc.ListAll(ctx, cr.repo, folder)
	if err != nil {
		return nil, err
	}
	d := &document{id: docID, meta: &docMeta{DocID: docID, ContRep: cr.id, Components: map[string]*compMeta{}}}
	for i := range items {
		if items[i].Name == sidecarName {
			d.sidecar = &items[i]
		}
	}
	if d.sidecar != nil {
		if m, err := s.readSidecar(ctx, cr, d.sidecar); err == nil {
			d.meta = m
		}
	}
	for i := range items {
		it := items[i]
		if it.Folder != nil || it.Name == sidecarName {
			continue
		}
		id := decodeName(it.Name)
		c := &component{id: id, item: it, meta: compMeta{Created: it.CreatedDateTime, Modified: it.LastModifiedDateTime}}
		if m := d.meta.Components[id]; m != nil {
			c.meta = *m
		}
		d.comps = append(d.comps, c)
	}
	sort.Slice(d.comps, func(i, j int) bool { return d.comps[i].id < d.comps[j].id })
	if d.meta.Created.IsZero() {
		d.meta.Created = d.created()
	}
	s.cache.put(key, d)
	return d, nil
}

func (s *docStore) readSidecar(ctx context.Context, cr *contentRep, it *graph.DriveItem) (*docMeta, error) {
	resp, err := s.svc.Open(ctx, cr.repo, it, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m docMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&m); err != nil {
		return nil, err
	}
	if m.Components == nil {
		m.Components = map[string]*compMeta{}
	}
	return &m, nil
}

func (s *docStore) writeSidecar(ctx context.Context, cr *contentRep, m *docMeta) error {
	folder, err := cr.docFolder(m.DocID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.svc.Upload(ctx, cr.repo, storage.UploadRequest{
		Folder: folder, Name: sidecarName, Conflict: graph.ConflictReplace,
		Body: bytes.NewReader(b), Size: int64(len(b)),
	})
	return err
}

// ensureShard creates the shard folder once per process so concurrent
// first writes into a shard do not race on implicit folder creation.
func (s *docStore) ensureShard(ctx context.Context, cr *contentRep, docID string) error {
	shard := shardOf(docID)
	cr.shardsMu.Lock()
	known, baseOK := cr.shards[shard], cr.baseOK
	cr.shardsMu.Unlock()
	if known {
		return nil
	}
	if !baseOK {
		if err := s.svc.EnsureFolder(ctx, cr.repo, cr.folder, false); err != nil {
			return err
		}
	}
	if err := s.svc.EnsureFolder(ctx, cr.repo, cr.folder+"/"+shard, true); err != nil {
		return err
	}
	cr.shardsMu.Lock()
	cr.baseOK = true
	cr.shards[shard] = true
	cr.shardsMu.Unlock()
	return nil
}

// putComponent uploads a component. With mustBeNew, an existing component
// yields errCompExists instead of being overwritten.
func (s *docStore) putComponent(ctx context.Context, cr *contentRep, docID, compID string, body io.Reader, size int64, mustBeNew bool) (*graph.DriveItem, error) {
	folder, err := cr.docFolder(docID)
	if err != nil {
		return nil, err
	}
	name, err := encodeName(compID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureShard(ctx, cr, docID); err != nil {
		return nil, err
	}
	conflict := graph.ConflictReplace
	if mustBeNew {
		conflict = graph.ConflictFail
	}
	it, err := s.svc.Upload(ctx, cr.repo, storage.UploadRequest{Folder: folder, Name: name, Conflict: conflict, Body: body, Size: size})
	if graph.IsConflict(err) {
		if _, gerr := s.svc.GetByPath(ctx, cr.repo, folder+"/"+name); gerr == nil {
			return nil, errCompExists
		}
		// 409 without an existing file: a parallel request created the
		// document folder at the same moment.
		return nil, errBusy
	}
	return it, err
}

func (s *docStore) replaceComponent(ctx context.Context, cr *contentRep, c *component, body io.Reader, size int64) (*graph.DriveItem, error) {
	return s.svc.Replace(ctx, cr.repo, c.item.ID, "", body, size)
}

func (s *docStore) deleteComponent(ctx context.Context, cr *contentRep, c *component) error {
	return s.svc.Delete(ctx, cr.repo, c.item.ID, "")
}

func (s *docStore) deleteDocument(ctx context.Context, cr *contentRep, docID string) error {
	folder, err := cr.docFolder(docID)
	if err != nil {
		return err
	}
	it, err := s.svc.GetByPath(ctx, cr.repo, folder)
	if err != nil {
		return err
	}
	return s.svc.Delete(ctx, cr.repo, it.ID, "")
}

func (s *docStore) createEmpty(ctx context.Context, cr *contentRep, docID string) error {
	folder, err := cr.docFolder(docID)
	if err != nil {
		return err
	}
	if err := s.ensureShard(ctx, cr, docID); err != nil {
		return err
	}
	return s.svc.EnsureFolder(ctx, cr.repo, folder, true)
}

func (s *docStore) invalidate(cr *contentRep, docID string) { s.cache.drop(cacheKey(cr, docID)) }
