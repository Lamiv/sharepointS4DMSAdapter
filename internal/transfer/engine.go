// Package transfer moves document bytes between callers and SharePoint. It
// picks single-request or chunked upload sessions, bounds memory with a
// process-wide budget, spools bodies of unknown length to disk, and streams
// downloads with Range support.
package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"sharepointadapter/internal/graph"
	"sharepointadapter/internal/observability"
)

var ErrTooLarge = errors.New("upload exceeds maximum size")

type Options struct {
	SimpleUploadMax int64
	ChunkSize       int64
	MemoryBudget    int64
	MaxUpload       int64
	SpoolDir        string
}

type Engine struct {
	g      *graph.Client
	opts   Options
	budget *semaphore.Weighted

	chunkPool  sync.Pool // []byte of ChunkSize
	simplePool sync.Pool // []byte of SimpleUploadMax
	copyPool   sync.Pool // []byte of 256 KiB for streaming downloads
}

func New(g *graph.Client, opts Options) *Engine {
	e := &Engine{g: g, opts: opts, budget: semaphore.NewWeighted(opts.MemoryBudget)}
	e.chunkPool.New = func() any { b := make([]byte, opts.ChunkSize); return &b }
	e.simplePool.New = func() any { b := make([]byte, opts.SimpleUploadMax); return &b }
	e.copyPool.New = func() any { b := make([]byte, 256<<10); return &b }
	return e
}

// Target identifies where an upload goes: a new file by path, or new
// content for an existing item.
type Target struct {
	Drive    string
	Path     string // drive-relative path incl. file name (new file)
	ItemID   string // existing item (replace content)
	Conflict graph.ConflictBehavior
	IfMatch  string
}

func (e *Engine) reserve(ctx context.Context, n int64) (func(), error) {
	if !e.budget.TryAcquire(n) {
		start := time.Now()
		if err := e.budget.Acquire(ctx, n); err != nil {
			return nil, err
		}
		observability.TransferBufferWait.Observe(time.Since(start).Seconds())
	}
	return func() { e.budget.Release(n) }, nil
}

// Upload stores body at t. size is the exact body length, or -1 if unknown.
func (e *Engine) Upload(ctx context.Context, t Target, body io.Reader, size int64) (*graph.DriveItem, error) {
	if size > e.opts.MaxUpload {
		return nil, ErrTooLarge
	}
	if size >= 0 && size <= e.opts.SimpleUploadMax {
		return e.uploadSimple(ctx, t, body, size)
	}
	if size > 0 {
		return e.uploadChunked(ctx, t, body, size)
	}
	return e.uploadUnknown(ctx, t, body)
}

func (e *Engine) uploadSimple(ctx context.Context, t Target, body io.Reader, size int64) (*graph.DriveItem, error) {
	release, err := e.reserve(ctx, max(size, 1))
	if err != nil {
		return nil, err
	}
	defer release()
	bp := e.simplePool.Get().(*[]byte)
	defer e.simplePool.Put(bp)
	buf := (*bp)[:size]
	if _, err := io.ReadFull(body, buf); err != nil {
		return nil, fmt.Errorf("read upload body: %w", err)
	}
	return e.putSmall(ctx, t, buf)
}

func (e *Engine) putSmall(ctx context.Context, t Target, data []byte) (*graph.DriveItem, error) {
	observability.TransfersActive.WithLabelValues("upload", "simple").Inc()
	defer observability.TransfersActive.WithLabelValues("upload", "simple").Dec()
	var it *graph.DriveItem
	var err error
	if t.ItemID != "" {
		it, err = e.g.ReplaceSmall(ctx, t.Drive, t.ItemID, data, t.IfMatch)
	} else {
		it, err = e.g.UploadSmallByPath(ctx, t.Drive, t.Path, t.Conflict, data)
	}
	if err == nil {
		observability.BytesTransferred.WithLabelValues("upload").Add(float64(len(data)))
	}
	return it, err
}

// uploadChunked streams body through one reusable chunk buffer, so memory
// per transfer is ChunkSize regardless of file size.
func (e *Engine) uploadChunked(ctx context.Context, t Target, body io.Reader, size int64) (*graph.DriveItem, error) {
	var sess *graph.UploadSession
	var err error
	if t.ItemID != "" {
		sess, err = e.g.CreateUploadSessionForItem(ctx, t.Drive, t.ItemID, t.IfMatch)
	} else {
		sess, err = e.g.CreateUploadSessionByPath(ctx, t.Drive, t.Path, t.Conflict)
	}
	if err != nil {
		return nil, err
	}
	observability.TransfersActive.WithLabelValues("upload", "session").Inc()
	defer observability.TransfersActive.WithLabelValues("upload", "session").Dec()

	chunk := min(e.opts.ChunkSize, size)
	release, err := e.reserve(ctx, chunk)
	if err != nil {
		e.g.CancelUploadSession(context.WithoutCancel(ctx), sess)
		return nil, err
	}
	defer release()
	bp := e.chunkPool.Get().(*[]byte)
	defer e.chunkPool.Put(bp)

	var offset int64
	for offset < size {
		n := min(e.opts.ChunkSize, size-offset)
		buf := (*bp)[:n]
		if _, err := io.ReadFull(body, buf); err != nil {
			e.g.CancelUploadSession(context.WithoutCancel(ctx), sess)
			return nil, fmt.Errorf("read upload body at %d: %w", offset, err)
		}
		it, err := e.g.UploadChunk(ctx, sess, offset, buf, size)
		if err != nil {
			e.g.CancelUploadSession(context.WithoutCancel(ctx), sess)
			return nil, err
		}
		observability.BytesTransferred.WithLabelValues("upload").Add(float64(n))
		offset += n
		if it != nil && it.ID != "" {
			return it, nil
		}
	}
	return nil, errors.New("upload session finished without returning an item")
}

// uploadUnknown handles bodies without Content-Length (chunked encoding or
// multipart parts): small bodies stay in memory, large ones spool to disk.
func (e *Engine) uploadUnknown(ctx context.Context, t Target, body io.Reader) (*graph.DriveItem, error) {
	release, err := e.reserve(ctx, e.opts.SimpleUploadMax)
	if err != nil {
		return nil, err
	}
	bp := e.simplePool.Get().(*[]byte)
	buf := *bp
	n, err := io.ReadFull(body, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		defer func() { e.simplePool.Put(bp); release() }()
		return e.putSmall(ctx, t, buf[:n])
	}
	if err != nil {
		e.simplePool.Put(bp)
		release()
		return nil, fmt.Errorf("read upload body: %w", err)
	}

	f, err := os.CreateTemp(e.opts.SpoolDir, "upload-*")
	if err != nil {
		e.simplePool.Put(bp)
		release()
		return nil, fmt.Errorf("spool: %w", err)
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	_, werr := f.Write(buf[:n])
	e.simplePool.Put(bp)
	release()
	if werr != nil {
		return nil, fmt.Errorf("spool: %w", werr)
	}
	limit := e.opts.MaxUpload - int64(n)
	copied, err := io.Copy(f, io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	if copied > limit {
		return nil, ErrTooLarge
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return e.uploadChunked(ctx, t, f, int64(n)+copied)
}

// ---- downloads ----

// DownloadResult describes what was written to the client.
type DownloadResult struct {
	Status int
	Bytes  int64
}

// passthroughHeaders are copied from SharePoint to the caller.
var passthroughHeaders = []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified"}

// Stream copies the item content (optionally a byte range) to w. Response
// headers set by the caller (Content-Type, ETag, Content-Disposition) are
// kept. refresh is called once to obtain a new download URL if the cached
// one has expired.
func (e *Engine) Stream(ctx context.Context, w http.ResponseWriter, downloadURL, rangeHdr string, refresh func() (string, error)) (DownloadResult, error) {
	resp, err := e.g.OpenDownload(ctx, downloadURL, rangeHdr)
	if err != nil && refresh != nil && isExpired(err) {
		if downloadURL, err = refresh(); err == nil {
			resp, err = e.g.OpenDownload(ctx, downloadURL, rangeHdr)
		}
	}
	if err != nil {
		return DownloadResult{}, err
	}
	defer resp.Body.Close()
	observability.TransfersActive.WithLabelValues("download", "stream").Inc()
	defer observability.TransfersActive.WithLabelValues("download", "stream").Dec()

	for _, h := range passthroughHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(resp.StatusCode)

	bp := e.copyPool.Get().(*[]byte)
	defer e.copyPool.Put(bp)
	n, err := io.CopyBuffer(writerOnly{w}, resp.Body, *bp)
	observability.BytesTransferred.WithLabelValues("download").Add(float64(n))
	return DownloadResult{Status: resp.StatusCode, Bytes: n}, err
}

// StreamResponse copies an already-open Graph response (e.g. thumbnails).
func (e *Engine) StreamResponse(w http.ResponseWriter, resp *http.Response) (int64, error) {
	defer resp.Body.Close()
	for _, h := range append(passthroughHeaders, "Content-Type") {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	bp := e.copyPool.Get().(*[]byte)
	defer e.copyPool.Put(bp)
	n, err := io.CopyBuffer(writerOnly{w}, resp.Body, *bp)
	observability.BytesTransferred.WithLabelValues("download").Add(float64(n))
	return n, err
}

// isExpired reports whether a pre-authenticated URL was rejected because
// its embedded token expired.
func isExpired(err error) bool {
	s := graph.StatusOf(err)
	return s == http.StatusUnauthorized || s == http.StatusForbidden || s == http.StatusNotFound
}

// writerOnly hides ReadFrom so io.CopyBuffer uses our pooled buffer.
type writerOnly struct{ io.Writer }
