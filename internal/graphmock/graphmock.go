// Package graphmock emulates the subset of Microsoft Graph / Entra ID used
// by the adapter (token endpoint, drive items, upload sessions, downloads,
// thumbnails). It is for development, integration and load testing only.
package graphmock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type item struct {
	ID       string
	Drive    string
	Name     string
	ParentID string
	Folder   bool
	Size     int64
	Version  int
	// Discarded items had their bytes dropped (RetainBytes exceeded);
	// downloads return filler bytes of the recorded size.
	Discarded bool
	Created   time.Time
	Modified  time.Time
}

type session struct {
	ID       string
	Drive    string
	ItemID   string // replace existing
	ParentID string
	Name     string
	Conflict string
	File     *os.File
	Next     int64
	Total    int64
}

type server struct {
	dataDir      string
	latencyMin   time.Duration
	latencyMax   time.Duration
	throttleRate float64
	retainMax    int64
	retained     atomic.Int64

	mu       sync.RWMutex
	items    map[string]*item
	children map[string]map[string]string // parentID -> lower(name) -> id
	roots    map[string]string            // drive -> root id
	sessions map[string]*session
}

// Options configures the emulator.
type Options struct {
	DataDir      string
	LatencyMin   time.Duration
	LatencyMax   time.Duration
	ThrottleRate float64
	Drives       []string
	// RetainBytes caps stored blob bytes (0 = unlimited). Beyond the cap,
	// upload sizes are still validated but content is discarded, so long
	// load tests do not exhaust the emulator's disk.
	RetainBytes int64
}

// New returns an http.Handler emulating Graph, Entra ID token endpoint,
// upload sessions and pre-authenticated download URLs.
func New(o Options) (http.Handler, error) {
	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		return nil, err
	}
	s := &server{
		dataDir:      o.DataDir,
		latencyMin:   o.LatencyMin,
		latencyMax:   o.LatencyMax,
		throttleRate: o.ThrottleRate,
		retainMax:    o.RetainBytes,
		items:        map[string]*item{},
		children:     map[string]map[string]string{},
		roots:        map[string]string{},
		sessions:     map[string]*session{},
	}
	if len(o.Drives) == 0 {
		o.Drives = []string{"drive1"}
	}
	for _, d := range o.Drives {
		s.ensureRoot(d)
	}
	return s, nil
}

// AddFile seeds a file at a drive-relative path (tests).
func (s *server) AddFile(drive, path string, data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, name := splitTarget("path:" + strings.Trim(path, "/"))
	it, _, _ := s.place(drive, dir, name, "replace")
	_ = os.WriteFile(s.blobPath(it.ID), data, 0o644)
	it.Size, it.Version = int64(len(data)), it.Version+1
	return it.ID
}

// Seeder is implemented by the handler returned from New.
type Seeder interface {
	AddFile(drive, path string, data []byte) string
}

func newID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return "01" + strings.ToUpper(hex.EncodeToString(b[:]))
}

func (s *server) ensureRoot(drive string) string {
	if id, ok := s.roots[drive]; ok {
		return id
	}
	id := "root-" + drive
	now := time.Now().UTC()
	s.items[id] = &item{ID: id, Drive: drive, Name: "root", Folder: true, Created: now, Modified: now, Version: 1}
	s.children[id] = map[string]string{}
	s.roots[drive] = id
	return id
}

func (s *server) delay() {
	if s.latencyMax <= 0 {
		return
	}
	d := s.latencyMin
	if s.latencyMax > s.latencyMin {
		d += time.Duration(mrand.Int64N(int64(s.latencyMax - s.latencyMin)))
	}
	time.Sleep(d)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	switch {
	case strings.HasSuffix(p, "/oauth2/v2.0/token") && r.Method == http.MethodPost:
		writeJSON(w, 200, map[string]any{"token_type": "Bearer", "expires_in": 3599, "access_token": "mock-" + newID()})
	case strings.HasPrefix(p, "/upload/"):
		s.delay()
		s.handleSession(w, r, strings.TrimPrefix(p, "/upload/"))
	case strings.HasPrefix(p, "/download/"):
		s.handleDownload(w, r, strings.TrimPrefix(p, "/download/"))
	case strings.HasPrefix(p, "/v1.0/"):
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer mock-") {
			graphError(w, 401, "InvalidAuthenticationToken", "Access token is empty or invalid.")
			return
		}
		s.delay()
		if s.throttleRate > 0 && mrand.Float64() < s.throttleRate {
			w.Header().Set("Retry-After", "1")
			graphError(w, 429, "activityLimitReached", "The request has been throttled")
			return
		}
		s.handleGraph(w, r, strings.TrimPrefix(p, "/v1.0/"))
	case p == "/healthz":
		w.Write([]byte("ok"))
	default:
		graphError(w, 404, "itemNotFound", "unknown endpoint")
	}
}

// handleGraph parses /drives/{d}/(root|root:/path:|items/{id})[/suffix].
func (s *server) handleGraph(w http.ResponseWriter, r *http.Request, p string) {
	if !strings.HasPrefix(p, "drives/") {
		graphError(w, 404, "itemNotFound", "only /drives is emulated")
		return
	}
	rest := strings.TrimPrefix(p, "drives/")
	drive, rest, _ := strings.Cut(rest, "/")
	drive, _ = url.PathUnescape(drive)
	s.mu.Lock()
	_, known := s.roots[drive]
	s.mu.Unlock()
	if !known {
		graphError(w, 404, "itemNotFound", "drive not found")
		return
	}
	if rest == "" {
		writeJSON(w, 200, map[string]any{"id": drive, "driveType": "documentLibrary"})
		return
	}

	var target, suffix string // target: "id:<id>" or "path:<path>"
	switch {
	case strings.HasPrefix(rest, "root:"):
		pathPart, suf, _ := strings.Cut(strings.TrimPrefix(rest, "root:"), ":")
		segs := strings.Split(strings.Trim(pathPart, "/"), "/")
		for i, sg := range segs {
			segs[i], _ = url.PathUnescape(sg)
		}
		target, suffix = "path:"+strings.Join(segs, "/"), strings.TrimPrefix(suf, "/")
	case rest == "root" || strings.HasPrefix(rest, "root/"):
		target, suffix = "path:", strings.TrimPrefix(strings.TrimPrefix(rest, "root"), "/")
	case strings.HasPrefix(rest, "items/"):
		id, suf, _ := strings.Cut(strings.TrimPrefix(rest, "items/"), "/")
		id, _ = url.PathUnescape(id)
		target, suffix = "id:"+id, suf
	default:
		graphError(w, 404, "itemNotFound", "unsupported path")
		return
	}
	suffix, _ = url.PathUnescape(suffix)

	switch {
	case suffix == "" && r.Method == http.MethodGet:
		s.withItem(w, drive, target, func(it *item) { writeJSON(w, 200, s.toJSON(r, it)) })
	case suffix == "" && r.Method == http.MethodDelete:
		s.deleteItem(w, drive, target)
	case suffix == "children" && r.Method == http.MethodGet:
		s.listChildren(w, r, drive, target)
	case suffix == "children" && r.Method == http.MethodPost:
		s.createChild(w, r, drive, target)
	case suffix == "content" && r.Method == http.MethodPut:
		s.putContent(w, r, drive, target)
	case suffix == "content" && r.Method == http.MethodGet:
		s.withItem(w, drive, target, func(it *item) {
			http.Redirect(w, r, s.downloadURL(r, it), http.StatusFound)
		})
	case suffix == "createUploadSession" && r.Method == http.MethodPost:
		s.createSession(w, r, drive, target)
	case suffix == "versions" && r.Method == http.MethodGet:
		s.withItem(w, drive, target, func(it *item) {
			writeJSON(w, 200, map[string]any{"value": []any{map[string]any{
				"id": fmt.Sprintf("%d.0", it.Version), "lastModifiedDateTime": it.Modified, "size": it.Size,
			}}})
		})
	case strings.HasPrefix(suffix, "search(q='") && r.Method == http.MethodGet:
		q := strings.TrimSuffix(strings.TrimPrefix(suffix, "search(q='"), "')")
		s.search(w, r, drive, target, strings.ReplaceAll(q, "''", "'"))
	case strings.HasPrefix(suffix, "thumbnails/0/") && strings.HasSuffix(suffix, "/content"):
		s.withItem(w, drive, target, func(it *item) {
			w.Header().Set("Content-Type", "image/png")
			w.Write(tinyPNG)
		})
	default:
		graphError(w, 400, "invalidRequest", "unsupported operation "+r.Method+" "+suffix)
	}
}

// resolve finds an item; caller holds the lock.
func (s *server) resolve(drive, target string) *item {
	if id, ok := strings.CutPrefix(target, "id:"); ok {
		if it := s.items[id]; it != nil && it.Drive == drive {
			return it
		}
		return nil
	}
	p := strings.TrimPrefix(target, "path:")
	cur := s.items[s.roots[drive]]
	if p == "" {
		return cur
	}
	for _, seg := range strings.Split(p, "/") {
		id, ok := s.children[cur.ID][strings.ToLower(seg)]
		if !ok {
			return nil
		}
		cur = s.items[id]
	}
	return cur
}

func (s *server) withItem(w http.ResponseWriter, drive, target string, fn func(*item)) {
	s.mu.RLock()
	it := s.resolve(drive, target)
	var cp item
	if it != nil {
		cp = *it
	}
	s.mu.RUnlock()
	if it == nil {
		graphError(w, 404, "itemNotFound", "The resource could not be found.")
		return
	}
	fn(&cp)
}

func (s *server) pathOf(it *item) string {
	var segs []string
	for cur := it; cur != nil && cur.ParentID != ""; cur = s.items[cur.ParentID] {
		segs = append([]string{cur.Name}, segs...)
	}
	return "/" + strings.Join(segs, "/")
}

func (s *server) downloadURL(r *http.Request, it *item) string {
	return fmt.Sprintf("http://%s/download/%s?v=%d&tempauth=%s", r.Host, url.PathEscape(it.ID), it.Version, newID())
}

func (s *server) toJSON(r *http.Request, it *item) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.toJSONLocked(r, it)
}

func (s *server) toJSONLocked(r *http.Request, it *item) map[string]any {
	m := map[string]any{
		"id":                   it.ID,
		"name":                 it.Name,
		"eTag":                 fmt.Sprintf(`"{%s},%d"`, it.ID, it.Version),
		"cTag":                 fmt.Sprintf(`"c:{%s},%d"`, it.ID, it.Version),
		"size":                 it.Size,
		"createdDateTime":      it.Created,
		"lastModifiedDateTime": it.Modified,
		"webUrl":               "https://contoso.sharepoint.com/sites/sap/Shared%20Documents" + s.pathOf(it),
		"createdBy":            map[string]any{"application": map[string]any{"displayName": "SAP Adapter"}},
		"lastModifiedBy":       map[string]any{"application": map[string]any{"displayName": "SAP Adapter"}},
	}
	if it.ParentID != "" {
		parentPath := "/drives/" + it.Drive + "/root:" + strings.TrimSuffix(s.pathOf(s.items[it.ParentID]), "/")
		m["parentReference"] = map[string]any{"driveId": it.Drive, "id": it.ParentID, "path": parentPath}
	}
	if it.Folder {
		m["folder"] = map[string]any{"childCount": len(s.children[it.ID])}
	} else {
		m["file"] = map[string]any{"mimeType": mimeOf(it.Name)}
		m["@microsoft.graph.downloadUrl"] = s.downloadURL(r, it)
	}
	return m
}

func mimeOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".txt":
		return "text/plain"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}
	return "application/octet-stream"
}

func (s *server) listChildren(w http.ResponseWriter, r *http.Request, drive, target string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	parent := s.resolve(drive, target)
	if parent == nil || !parent.Folder {
		graphError(w, 404, "itemNotFound", "The resource could not be found.")
		return
	}
	ids := make([]string, 0, len(s.children[parent.ID]))
	for _, id := range s.children[parent.ID] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return s.items[ids[i]].Name < s.items[ids[j]].Name })
	top, _ := strconv.Atoi(r.URL.Query().Get("$top"))
	skip, _ := strconv.Atoi(r.URL.Query().Get("$skiptoken"))
	if top <= 0 {
		top = 200
	}
	end := min(skip+top, len(ids))
	out := []any{}
	for _, id := range ids[min(skip, len(ids)):end] {
		out = append(out, s.toJSONLocked(r, s.items[id]))
	}
	resp := map[string]any{"value": out}
	if end < len(ids) {
		q := r.URL.Query()
		q.Set("$skiptoken", strconv.Itoa(end))
		resp["@odata.nextLink"] = "http://" + r.Host + r.URL.Path + "?" + q.Encode()
	}
	writeJSON(w, 200, resp)
}

func (s *server) search(w http.ResponseWriter, r *http.Request, drive, target, q string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	base := s.resolve(drive, target)
	if base == nil {
		graphError(w, 404, "itemNotFound", "The resource could not be found.")
		return
	}
	prefix := strings.TrimSuffix(s.pathOf(base), "/") + "/"
	out := []any{}
	q = strings.ToLower(q)
	for _, it := range s.items {
		if it.Drive == drive && it.ParentID != "" && strings.Contains(strings.ToLower(it.Name), q) && strings.HasPrefix(s.pathOf(it), prefix) {
			out = append(out, s.toJSONLocked(r, it))
			if len(out) >= 200 {
				break
			}
		}
	}
	writeJSON(w, 200, map[string]any{"value": out})
}

// ensureFolders creates missing folders along p (caller holds write lock).
func (s *server) ensureFolders(drive, p string) (*item, error) {
	cur := s.items[s.roots[drive]]
	if p == "" {
		return cur, nil
	}
	for _, seg := range strings.Split(p, "/") {
		if id, ok := s.children[cur.ID][strings.ToLower(seg)]; ok {
			cur = s.items[id]
			if !cur.Folder {
				return nil, fmt.Errorf("%s is a file", seg)
			}
			continue
		}
		now := time.Now().UTC()
		f := &item{ID: newID(), Drive: drive, Name: seg, ParentID: cur.ID, Folder: true, Created: now, Modified: now, Version: 1}
		s.items[f.ID] = f
		s.children[f.ID] = map[string]string{}
		s.children[cur.ID][strings.ToLower(seg)] = f.ID
		cur = f
	}
	return cur, nil
}

func splitTarget(target string) (dir, name string) {
	p := strings.TrimPrefix(target, "path:")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

// place resolves the destination for a new file honoring conflictBehavior.
// It returns an existing item to overwrite, or a new item (caller holds lock).
func (s *server) place(drive, dir, name, conflict string) (*item, int, string) {
	parent, err := s.ensureFolders(drive, dir)
	if err != nil {
		return nil, 409, err.Error()
	}
	if id, ok := s.children[parent.ID][strings.ToLower(name)]; ok {
		switch conflict {
		case "replace", "":
			if s.items[id].Folder {
				return nil, 409, "a folder with that name exists"
			}
			return s.items[id], 0, ""
		case "fail":
			return nil, 409, "nameAlreadyExists"
		default: // rename
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			for i := 1; ; i++ {
				cand := fmt.Sprintf("%s %d%s", base, i, ext)
				if _, taken := s.children[parent.ID][strings.ToLower(cand)]; !taken {
					name = cand
					break
				}
			}
		}
	}
	now := time.Now().UTC()
	it := &item{ID: newID(), Drive: drive, Name: name, ParentID: parent.ID, Created: now, Modified: now}
	s.items[it.ID] = it
	s.children[parent.ID][strings.ToLower(name)] = it.ID
	return it, 0, ""
}

func (s *server) blobPath(id string) string { return filepath.Join(s.dataDir, id) }

// store moves a completed temp file into place for it, or discards it when
// the retention cap is reached (caller holds the write lock).
func (s *server) store(it *item, tmp string, size int64) {
	if !it.Discarded {
		s.retained.Add(-it.Size)
	}
	if s.retainMax > 0 && s.retained.Load()+size > s.retainMax {
		os.Remove(tmp)
		os.Remove(s.blobPath(it.ID))
		it.Discarded = true
		return
	}
	os.Rename(tmp, s.blobPath(it.ID))
	it.Discarded = false
	s.retained.Add(size)
}

// zeroReader serves filler content for discarded items.
type zeroReader struct{ size, off int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.off >= z.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), z.size-z.off))
	clear(p[:n])
	z.off += int64(n)
	return n, nil
}

func (z *zeroReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		z.off = offset
	case io.SeekCurrent:
		z.off += offset
	case io.SeekEnd:
		z.off = z.size + offset
	}
	return z.off, nil
}

func (s *server) putContent(w http.ResponseWriter, r *http.Request, drive, target string) {
	tmp, err := os.CreateTemp(s.dataDir, "put-*")
	if err != nil {
		graphError(w, 500, "generalException", err.Error())
		return
	}
	n, err := io.Copy(tmp, r.Body)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		graphError(w, 400, "invalidRequest", err.Error())
		return
	}
	s.mu.Lock()
	var it *item
	if strings.HasPrefix(target, "id:") {
		it = s.resolve(drive, target)
		if it == nil {
			s.mu.Unlock()
			os.Remove(tmp.Name())
			graphError(w, 404, "itemNotFound", "The resource could not be found.")
			return
		}
	} else {
		dir, name := splitTarget(target)
		var code int
		var msg string
		it, code, msg = s.place(drive, dir, name, r.URL.Query().Get("@microsoft.graph.conflictBehavior"))
		if it == nil {
			s.mu.Unlock()
			os.Remove(tmp.Name())
			graphError(w, code, "nameAlreadyExists", msg)
			return
		}
	}
	if m := r.Header.Get("If-Match"); m != "" && m != fmt.Sprintf(`"{%s},%d"`, it.ID, it.Version) {
		s.mu.Unlock()
		os.Remove(tmp.Name())
		graphError(w, 412, "resourceModified", "ETag mismatch")
		return
	}
	created := it.Version == 0
	s.store(it, tmp.Name(), n)
	it.Size, it.Version, it.Modified = n, it.Version+1, time.Now().UTC()
	body := s.toJSONLocked(r, it)
	s.mu.Unlock()
	code := 200
	if created {
		code = 201
	}
	writeJSON(w, code, body)
}

func (s *server) createChild(w http.ResponseWriter, r *http.Request, drive, target string) {
	var body struct {
		Name     string `json:"name"`
		Conflict string `json:"@microsoft.graph.conflictBehavior"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		graphError(w, 400, "invalidRequest", "name required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parent := s.resolve(drive, target)
	if parent == nil || !parent.Folder {
		graphError(w, 404, "itemNotFound", "The resource could not be found.")
		return
	}
	if _, ok := s.children[parent.ID][strings.ToLower(body.Name)]; ok && body.Conflict == "fail" {
		graphError(w, 409, "nameAlreadyExists", "The specified item name already exists.")
		return
	}
	f, err := s.ensureFolders(drive, strings.TrimPrefix(s.pathOf(parent)+"/"+body.Name, "/"))
	if err != nil {
		graphError(w, 409, "nameAlreadyExists", err.Error())
		return
	}
	writeJSON(w, 201, s.toJSONLocked(r, f))
}

func (s *server) deleteItem(w http.ResponseWriter, drive, target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.resolve(drive, target)
	if it == nil || it.ParentID == "" {
		graphError(w, 404, "itemNotFound", "The resource could not be found.")
		return
	}
	var drop func(id string)
	drop = func(id string) {
		for _, c := range s.children[id] {
			drop(c)
		}
		if it := s.items[id]; it != nil && !it.Discarded && !it.Folder {
			s.retained.Add(-it.Size)
		}
		delete(s.children, id)
		delete(s.items, id)
		os.Remove(s.blobPath(id))
	}
	delete(s.children[it.ParentID], strings.ToLower(it.Name))
	drop(it.ID)
	w.WriteHeader(204)
}

func (s *server) createSession(w http.ResponseWriter, r *http.Request, drive, target string) {
	var body struct {
		Item struct {
			Conflict string `json:"@microsoft.graph.conflictBehavior"`
		} `json:"item"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f, err := os.CreateTemp(s.dataDir, "session-*")
	if err != nil {
		graphError(w, 500, "generalException", err.Error())
		return
	}
	sess := &session{ID: newID(), Drive: drive, File: f, Total: -1, Conflict: body.Item.Conflict}
	s.mu.Lock()
	if strings.HasPrefix(target, "id:") {
		it := s.resolve(drive, target)
		if it == nil {
			s.mu.Unlock()
			f.Close()
			os.Remove(f.Name())
			graphError(w, 404, "itemNotFound", "The resource could not be found.")
			return
		}
		sess.ItemID = it.ID
	} else {
		dir, name := splitTarget(target)
		sess.ParentID, sess.Name = dir, name
	}
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"uploadUrl":          fmt.Sprintf("http://%s/upload/%s", r.Host, sess.ID),
		"expirationDateTime": time.Now().Add(time.Hour).UTC(),
		"nextExpectedRanges": []string{"0-"},
	})
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request, id string) {
	if r.Header.Get("Authorization") != "" {
		graphError(w, 401, "unauthenticated", "upload URLs must not carry Authorization")
		return
	}
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess == nil {
		graphError(w, 404, "itemNotFound", "upload session not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", sess.Next)}})
		return
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		sess.File.Close()
		os.Remove(sess.File.Name())
		w.WriteHeader(204)
		return
	case http.MethodPut:
	default:
		graphError(w, 405, "invalidRequest", "method not allowed")
		return
	}
	var start, end, total int64
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil {
		graphError(w, 400, "invalidRange", "bad Content-Range")
		return
	}
	if (end-start+1)%(320*1024) != 0 && end+1 != total {
		graphError(w, 400, "invalidRange", "fragment must be a multiple of 320 KiB")
		return
	}
	if start != sess.Next {
		graphError(w, 416, "invalidRange", "unexpected range")
		return
	}
	n, err := io.Copy(io.NewOffsetWriter(sess.File, start), io.LimitReader(r.Body, end-start+1))
	if err != nil || n != end-start+1 {
		graphError(w, 400, "invalidRequest", "short fragment")
		return
	}
	sess.Next, sess.Total = end+1, total
	if sess.Next < total {
		writeJSON(w, 202, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", sess.Next)}})
		return
	}
	sess.File.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	var it *item
	if sess.ItemID != "" {
		it = s.items[sess.ItemID]
	} else {
		var code int
		var msg string
		if it, code, msg = s.place(sess.Drive, sess.ParentID, sess.Name, sess.Conflict); it == nil {
			os.Remove(sess.File.Name())
			graphError(w, code, "nameAlreadyExists", msg)
			return
		}
	}
	if it == nil {
		graphError(w, 404, "itemNotFound", "item vanished")
		return
	}
	created := it.Version == 0
	s.store(it, sess.File.Name(), total)
	it.Size, it.Version, it.Modified = total, it.Version+1, time.Now().UTC()
	code := 200
	if created {
		code = 201
	}
	writeJSON(w, code, s.toJSONLocked(r, it))
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request, id string) {
	id, _ = url.PathUnescape(id)
	if r.URL.Query().Get("tempauth") == "" {
		graphError(w, 401, "unauthenticated", "missing tempauth")
		return
	}
	s.mu.RLock()
	it := s.items[id]
	var mod time.Time
	var discarded bool
	var size int64
	if it != nil {
		mod, discarded, size = it.Modified, it.Discarded, it.Size
	}
	s.mu.RUnlock()
	if it == nil {
		graphError(w, 404, "itemNotFound", "The resource could not be found.")
		return
	}
	if discarded {
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", mod, &zeroReader{size: size})
		return
	}
	f, err := os.Open(s.blobPath(id))
	if err != nil {
		graphError(w, 404, "itemNotFound", "blob missing")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", mod, f)
}

func graphError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// 1x1 transparent PNG.
var tinyPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00,
	0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d,
	0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}
