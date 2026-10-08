package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Identity struct {
	DisplayName string `json:"displayName,omitempty"`
	ID          string `json:"id,omitempty"`
}

type IdentitySet struct {
	User        *Identity `json:"user,omitempty"`
	Application *Identity `json:"application,omitempty"`
}

func (s *IdentitySet) Name() string {
	switch {
	case s == nil:
		return ""
	case s.User != nil && s.User.DisplayName != "":
		return s.User.DisplayName
	case s.Application != nil:
		return s.Application.DisplayName
	}
	return ""
}

type DriveItem struct {
	ID                   string       `json:"id"`
	Name                 string       `json:"name"`
	ETag                 string       `json:"eTag,omitempty"`
	CTag                 string       `json:"cTag,omitempty"`
	Size                 int64        `json:"size"`
	CreatedDateTime      time.Time    `json:"createdDateTime"`
	LastModifiedDateTime time.Time    `json:"lastModifiedDateTime"`
	WebURL               string       `json:"webUrl,omitempty"`
	CreatedBy            *IdentitySet `json:"createdBy,omitempty"`
	LastModifiedBy       *IdentitySet `json:"lastModifiedBy,omitempty"`
	File                 *struct {
		MimeType string `json:"mimeType"`
		Hashes   *struct {
			QuickXorHash string `json:"quickXorHash,omitempty"`
			SHA256Hash   string `json:"sha256Hash,omitempty"`
		} `json:"hashes,omitempty"`
	} `json:"file,omitempty"`
	Folder *struct {
		ChildCount int `json:"childCount"`
	} `json:"folder,omitempty"`
	ParentReference *struct {
		DriveID string `json:"driveId"`
		ID      string `json:"id"`
		Path    string `json:"path"`
	} `json:"parentReference,omitempty"`
	DownloadURL string `json:"@microsoft.graph.downloadUrl,omitempty"`
}

// Path returns the item's path relative to the drive root, e.g. "/SAP/DMS/a.pdf".
// Root returns "/".
func (it *DriveItem) Path() string {
	if it.ParentReference == nil || it.ParentReference.Path == "" {
		return "/"
	}
	_, parent, found := strings.Cut(it.ParentReference.Path, "root:")
	if !found {
		return "/" + it.Name
	}
	if un, err := url.PathUnescape(parent); err == nil {
		parent = un
	}
	return strings.TrimRight(parent, "/") + "/" + it.Name
}

type Version struct {
	ID                   string       `json:"id"`
	LastModifiedDateTime time.Time    `json:"lastModifiedDateTime"`
	Size                 int64        `json:"size"`
	LastModifiedBy       *IdentitySet `json:"lastModifiedBy,omitempty"`
}

// ConflictBehavior for uploads/creates: fail, replace or rename.
type ConflictBehavior string

const (
	ConflictFail    ConflictBehavior = "fail"
	ConflictReplace ConflictBehavior = "replace"
	ConflictRename  ConflictBehavior = "rename"
)

// ItemSelect is the projection used for metadata reads; it includes the
// pre-authenticated download URL so downloads need only one Graph call.
const ItemSelect = "id,name,eTag,cTag,size,createdDateTime,lastModifiedDateTime,webUrl,createdBy,lastModifiedBy,file,folder,parentReference,@microsoft.graph.downloadUrl"

// EscapePath escapes each segment of a drive-relative path for use in
// Graph path addressing (root:/a/b:/).
func EscapePath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

func itemByPathURL(drive, path, suffix string) string {
	p := strings.Trim(path, "/")
	if p == "" {
		if suffix == "" {
			return fmt.Sprintf("/drives/%s/root", url.PathEscape(drive))
		}
		return fmt.Sprintf("/drives/%s/root/%s", url.PathEscape(drive), suffix)
	}
	return fmt.Sprintf("/drives/%s/root:/%s:%s", url.PathEscape(drive), EscapePath(p), prefixed(suffix))
}

func itemURL(drive, id, suffix string) string {
	return fmt.Sprintf("/drives/%s/items/%s%s", url.PathEscape(drive), url.PathEscape(id), prefixed(suffix))
}

func prefixed(s string) string {
	if s == "" {
		return ""
	}
	return "/" + s
}

// ---- drive resolution ----

// ResolveDrive finds the drive ID for a site (by ID or URL) and optional library name.
func (c *Client) ResolveDrive(ctx context.Context, siteID, siteURL, driveName string) (string, error) {
	if siteID == "" {
		u, err := url.Parse(siteURL)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("invalid siteUrl %q", siteURL)
		}
		var site struct {
			ID string `json:"id"`
		}
		path := "/sites/" + u.Host
		if p := strings.Trim(u.Path, "/"); p != "" {
			path += ":/" + EscapePath(p)
		}
		resp, err := c.do(ctx, request{op: "site.get", method: http.MethodGet, url: path + "?$select=id"})
		if err != nil {
			return "", err
		}
		if err := decode(resp, &site); err != nil {
			return "", err
		}
		siteID = site.ID
	}
	if driveName == "" {
		var d struct {
			ID string `json:"id"`
		}
		resp, err := c.do(ctx, request{op: "drive.get", method: http.MethodGet, url: "/sites/" + url.PathEscape(siteID) + "/drive?$select=id"})
		if err != nil {
			return "", err
		}
		if err := decode(resp, &d); err != nil {
			return "", err
		}
		return d.ID, nil
	}
	var list struct {
		Value []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"value"`
	}
	resp, err := c.do(ctx, request{op: "drive.list", method: http.MethodGet, url: "/sites/" + url.PathEscape(siteID) + "/drives?$select=id,name"})
	if err != nil {
		return "", err
	}
	if err := decode(resp, &list); err != nil {
		return "", err
	}
	for _, d := range list.Value {
		if strings.EqualFold(d.Name, driveName) {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("document library %q not found in site %s", driveName, siteID)
}

// ---- metadata ----

func (c *Client) GetItem(ctx context.Context, drive, id string) (*DriveItem, error) {
	return c.getItem(ctx, itemURL(drive, id, "")+"?$select="+ItemSelect)
}

func (c *Client) GetItemByPath(ctx context.Context, drive, path string) (*DriveItem, error) {
	return c.getItem(ctx, itemByPathURL(drive, path, "")+"?$select="+ItemSelect)
}

func (c *Client) getItem(ctx context.Context, u string) (*DriveItem, error) {
	resp, err := c.do(ctx, request{op: "item.get", method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var it DriveItem
	return &it, decode(resp, &it)
}

type Page struct {
	Items    []DriveItem
	NextLink string
}

// ListChildren lists a folder by drive-relative path. Pass the previous
// page's NextLink to continue.
func (c *Client) ListChildren(ctx context.Context, drive, path string, top int, nextLink string) (*Page, error) {
	u := nextLink
	if u == "" {
		q := url.Values{"$select": {ItemSelect}}
		if top > 0 {
			q.Set("$top", strconv.Itoa(top))
		}
		u = itemByPathURL(drive, path, "children") + "?" + q.Encode()
	} else if !strings.HasPrefix(u, c.base+"/") {
		return nil, errors.New("invalid continuation link")
	}
	return c.page(ctx, "item.children", u)
}

// Search finds items below the folder at path whose name or content matches query.
func (c *Client) Search(ctx context.Context, drive, path, query string, top int, nextLink string) (*Page, error) {
	u := nextLink
	if u == "" {
		q := url.Values{"$select": {ItemSelect}}
		if top > 0 {
			q.Set("$top", strconv.Itoa(top))
		}
		esc := url.PathEscape(strings.ReplaceAll(query, "'", "''"))
		u = itemByPathURL(drive, path, "search(q='"+esc+"')") + "?" + q.Encode()
	} else if !strings.HasPrefix(u, c.base+"/") {
		return nil, errors.New("invalid continuation link")
	}
	return c.page(ctx, "drive.search", u)
}

func (c *Client) page(ctx context.Context, op, u string) (*Page, error) {
	resp, err := c.do(ctx, request{op: op, method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var body struct {
		Value    []DriveItem `json:"value"`
		NextLink string      `json:"@odata.nextLink"`
	}
	if err := decode(resp, &body); err != nil {
		return nil, err
	}
	return &Page{Items: body.Value, NextLink: body.NextLink}, nil
}

func (c *Client) Versions(ctx context.Context, drive, id string) ([]Version, error) {
	resp, err := c.do(ctx, request{op: "item.versions", method: http.MethodGet, url: itemURL(drive, id, "versions")})
	if err != nil {
		return nil, err
	}
	var body struct {
		Value []Version `json:"value"`
	}
	if err := decode(resp, &body); err != nil {
		return nil, err
	}
	return body.Value, nil
}

// ---- mutations ----

// CreateFolderPath creates every missing folder along path and returns the last one.
func (c *Client) CreateFolderPath(ctx context.Context, drive, path string) (*DriveItem, error) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	var item *DriveItem
	parent := ""
	for _, name := range segs {
		if name == "" {
			continue
		}
		body := map[string]any{"name": name, "folder": map[string]any{}, "@microsoft.graph.conflictBehavior": "fail"}
		resp, err := c.do(ctx, request{op: "folder.create", method: http.MethodPost, url: itemByPathURL(drive, parent, "children"), body: jsonBody(body), header: jsonHeader})
		full := strings.TrimLeft(parent+"/"+name, "/")
		switch {
		case err == nil:
			item = &DriveItem{}
			if err := decode(resp, item); err != nil {
				return nil, err
			}
		case IsConflict(err):
			if item, err = c.GetItemByPath(ctx, drive, full); err != nil {
				return nil, err
			}
			if item.Folder == nil {
				return nil, &Error{Status: http.StatusConflict, Code: "nameAlreadyExists", Message: full + " exists and is not a folder"}
			}
		default:
			return nil, err
		}
		parent = full
	}
	if item == nil {
		return c.GetItemByPath(ctx, drive, "")
	}
	return item, nil
}

// UploadSmallByPath uploads a file of up to a few MiB in one request.
// Missing parent folders are created by Graph.
func (c *Client) UploadSmallByPath(ctx context.Context, drive, path string, conflict ConflictBehavior, data []byte) (*DriveItem, error) {
	u := itemByPathURL(drive, path, "content") + "?@microsoft.graph.conflictBehavior=" + string(conflict)
	return c.putContent(ctx, u, data, "")
}

// ReplaceSmall replaces the content of an existing item (creates a new version).
func (c *Client) ReplaceSmall(ctx context.Context, drive, id string, data []byte, ifMatch string) (*DriveItem, error) {
	return c.putContent(ctx, itemURL(drive, id, "content"), data, ifMatch)
}

func (c *Client) putContent(ctx context.Context, u string, data []byte, ifMatch string) (*DriveItem, error) {
	h := http.Header{"Content-Type": {"application/octet-stream"}}
	if ifMatch != "" {
		h.Set("If-Match", ifMatch)
	}
	resp, err := c.do(ctx, request{op: "item.upload", method: http.MethodPut, url: u, body: bytesBody(data), header: h})
	if err != nil {
		return nil, err
	}
	var it DriveItem
	return &it, decode(resp, &it)
}

// UploadSession is a resumable upload; its URL is pre-authenticated.
type UploadSession struct {
	URL string
}

func (c *Client) CreateUploadSessionByPath(ctx context.Context, drive, path string, conflict ConflictBehavior) (*UploadSession, error) {
	body := map[string]any{"item": map[string]any{"@microsoft.graph.conflictBehavior": string(conflict)}}
	return c.createSession(ctx, itemByPathURL(drive, path, "createUploadSession"), body, "")
}

func (c *Client) CreateUploadSessionForItem(ctx context.Context, drive, id, ifMatch string) (*UploadSession, error) {
	body := map[string]any{"item": map[string]any{"@microsoft.graph.conflictBehavior": "replace"}}
	return c.createSession(ctx, itemURL(drive, id, "createUploadSession"), body, ifMatch)
}

func (c *Client) createSession(ctx context.Context, u string, body any, ifMatch string) (*UploadSession, error) {
	h := jsonHeader.Clone()
	if ifMatch != "" {
		h.Set("If-Match", ifMatch)
	}
	resp, err := c.do(ctx, request{op: "session.create", method: http.MethodPost, url: u, body: jsonBody(body), header: h})
	if err != nil {
		return nil, err
	}
	var s struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := decode(resp, &s); err != nil {
		return nil, err
	}
	if s.UploadURL == "" {
		return nil, errors.New("graph: upload session without uploadUrl")
	}
	return &UploadSession{URL: s.UploadURL}, nil
}

// UploadChunk sends bytes [offset, offset+len(data)) of total. It returns the
// completed item on the final chunk, otherwise nil.
func (c *Client) UploadChunk(ctx context.Context, s *UploadSession, offset int64, data []byte, total int64) (*DriveItem, error) {
	end := offset + int64(len(data)) - 1
	h := http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", offset, end, total)}}
	resp, err := c.do(ctx, request{op: "session.chunk", method: http.MethodPut, url: s.URL, body: bytesBody(data), header: h, noAuth: true})
	if err != nil {
		// A retried chunk that the server already accepted yields 416; check
		// the session and continue if it moved past this chunk.
		if StatusOf(err) == http.StatusRequestedRangeNotSatisfiable {
			if next, serr := c.sessionNext(ctx, s); serr == nil && next > end {
				return nil, nil
			}
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var it DriveItem
		return &it, decode(resp, &it)
	}
	_ = decode(resp, nil)
	return nil, nil
}

func (c *Client) sessionNext(ctx context.Context, s *UploadSession) (int64, error) {
	resp, err := c.do(ctx, request{op: "session.status", method: http.MethodGet, url: s.URL, noAuth: true})
	if err != nil {
		return 0, err
	}
	var st struct {
		NextExpectedRanges []string `json:"nextExpectedRanges"`
	}
	if err := decode(resp, &st); err != nil {
		return 0, err
	}
	if len(st.NextExpectedRanges) == 0 {
		return 0, errors.New("no expected ranges")
	}
	start, _, _ := strings.Cut(st.NextExpectedRanges[0], "-")
	return strconv.ParseInt(start, 10, 64)
}

// CancelUploadSession discards a partially uploaded session (best effort).
func (c *Client) CancelUploadSession(ctx context.Context, s *UploadSession) {
	resp, err := c.do(ctx, request{op: "session.cancel", method: http.MethodDelete, url: s.URL, noAuth: true, noRetry: true})
	if err == nil {
		_ = decode(resp, nil)
	}
}

func (c *Client) Delete(ctx context.Context, drive, id, ifMatch string) error {
	var h http.Header
	if ifMatch != "" {
		h = http.Header{"If-Match": {ifMatch}}
	}
	resp, err := c.do(ctx, request{op: "item.delete", method: http.MethodDelete, url: itemURL(drive, id, ""), header: h})
	if err != nil {
		return err
	}
	return decode(resp, nil)
}

// ---- content ----

// OpenDownload issues a GET against a pre-authenticated download URL,
// forwarding an optional Range header. The caller closes the body.
func (c *Client) OpenDownload(ctx context.Context, downloadURL, rangeHdr string) (*http.Response, error) {
	var h http.Header
	if rangeHdr != "" {
		h = http.Header{"Range": {rangeHdr}}
	}
	return c.do(ctx, request{op: "content.get", method: http.MethodGet, url: downloadURL, header: h, noAuth: true})
}

// OpenThumbnail streams a thumbnail rendition (small|medium|large) of an item.
func (c *Client) OpenThumbnail(ctx context.Context, drive, id, size string) (*http.Response, error) {
	return c.do(ctx, request{op: "thumbnail.get", method: http.MethodGet, url: itemURL(drive, id, "thumbnails/0/"+url.PathEscape(size)+"/content")})
}

// Ping verifies credentials and drive reachability.
func (c *Client) Ping(ctx context.Context, drive string) error {
	resp, err := c.do(ctx, request{op: "drive.ping", method: http.MethodGet, url: "/drives/" + url.PathEscape(drive) + "?$select=id", noRetry: true})
	if err != nil {
		return err
	}
	return decode(resp, nil)
}

// Drain discards and closes a response body.
func Drain(resp *http.Response) {
	if resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
	}
}
