// Package contentrepo implements the SAP Content Server HTTP interface
// (ArchiveLink / KPro "HTTP content server", pVersion 0045/0046/0047) on top
// of the shared SharePoint storage service, so SAP DMS, GOS and ArchiveLink
// can use SharePoint as a content repository configured in OAC0.
package contentrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sharepointadapter/internal/config"
	"sharepointadapter/internal/graph"
	"sharepointadapter/internal/observability"
	"sharepointadapter/internal/storage"
	"sharepointadapter/internal/transfer"
)

const defaultPVersion = "0046"

type Server struct {
	svc     *storage.Service
	cfg     config.ContentServerConfig
	reps    map[string]*contentRep
	order   []string
	certs   *certStore
	docs    *docStore
	allowed []*net.IPNet
	log     *slog.Logger
	version string
	now     func() time.Time
}

func New(ctx context.Context, svc *storage.Service, cfg config.ContentServerConfig, cacheTTL time.Duration, version string, log *slog.Logger) (*Server, error) {
	s := &Server{
		svc:     svc,
		cfg:     cfg,
		reps:    map[string]*contentRep{},
		docs:    &docStore{svc: svc, cache: &docCache{ttl: cacheTTL, m: map[string]docCacheEntry{}}},
		log:     log,
		version: version,
		now:     time.Now,
	}
	for _, rc := range cfg.Repositories {
		repo, err := svc.Repository(rc.Repository)
		if err != nil {
			return nil, fmt.Errorf("contRep %s: %w", rc.ContRep, err)
		}
		s.reps[rc.ContRep] = &contentRep{id: rc.ContRep, cfg: rc, repo: repo, folder: rc.Folder, shards: map[string]bool{}}
		s.order = append(s.order, rc.ContRep)
	}
	for _, cidr := range cfg.AllowedNetworks {
		if !strings.Contains(cidr, "/") {
			if strings.Contains(cidr, ":") {
				cidr += "/128"
			} else {
				cidr += "/32"
			}
		}
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("contentServer.allowedNetworks: %w", err)
		}
		s.allowed = append(s.allowed, n)
	}
	s.certs = newCertStore(svc, s.reps, log)
	if err := s.certs.reload(ctx, 0); err != nil {
		log.Warn("could not load content server certificates; will retry", "error", err)
	}
	return s, nil
}

// RefreshCertificates periodically reloads certificates so activations done
// through another adapter instance take effect.
func (s *Server) RefreshCertificates(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.certs.reload(ctx, every/2); err != nil {
				s.log.Warn("certificate reload failed", "error", err)
			}
		}
	}
}

// ---- request parsing ----

type params struct {
	cmd    string
	values map[string]string // lower-cased names
}

func (p params) get(name string) string { return p.values[strings.ToLower(name)] }

func parseParams(rawQuery string) params {
	p := params{values: map[string]string{}}
	for i, tok := range strings.Split(rawQuery, "&") {
		if tok == "" {
			continue
		}
		k, v, hasEq := strings.Cut(tok, "=")
		k, _ = url.QueryUnescape(k)
		if i == 0 && !hasEq {
			p.cmd = k
			continue
		}
		if dv, err := url.QueryUnescape(v); err == nil {
			v = dv
		}
		p.values[strings.ToLower(k)] = v
	}
	return p
}

// ---- errors ----

type csError struct {
	status int
	msg    string
}

func (e *csError) Error() string { return e.msg }

func errStatus(status int, format string, args ...any) error {
	return &csError{status: status, msg: fmt.Sprintf(format, args...)}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := http.StatusInternalServerError, "internal error"
	var ce *csError
	var ge *graph.Error
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ce):
		status, msg = ce.status, ce.msg
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, storage.ErrNotFound):
		status, msg = http.StatusNotFound, "document or component not found"
	case errors.Is(err, errDocExists), errors.Is(err, errCompExists):
		status, msg = http.StatusForbidden, err.Error()
	case errors.Is(err, errBusy):
		status, msg = http.StatusConflict, err.Error()
	case errors.Is(err, storage.ErrInvalid):
		status, msg = http.StatusBadRequest, err.Error()
	case errors.Is(err, storage.ErrReadOnly):
		status, msg = http.StatusForbidden, "repository is read-only"
	case errors.Is(err, transfer.ErrTooLarge), errors.As(err, &mbe):
		status, msg = http.StatusRequestEntityTooLarge, "content too large"
	case errors.As(err, &ge) && (ge.Status == http.StatusTooManyRequests || ge.Status == http.StatusServiceUnavailable):
		status, msg = http.StatusServiceUnavailable, "SharePoint is throttling requests; retry later"
		w.Header().Set("Retry-After", "5")
	case errors.As(err, &ge):
		status, msg = http.StatusInternalServerError, "SharePoint request failed"
	}
	if status >= 500 {
		s.log.Error("content server request failed", "request_id", observability.RequestID(r.Context()), "error", err)
	}
	h := w.Header()
	h.Set("X-ErrorDescription", msg)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Del("Content-Length")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, msg+"\r\n")
	}
}

// ---- dispatch ----

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	p := parseParams(r.URL.RawQuery)
	r.Pattern = "cs:" + p.cmd // metrics/log route label
	if !s.allowedClient(r) {
		s.fail(w, r, errStatus(http.StatusForbidden, "client address not allowed"))
		return
	}
	pv := p.get("pVersion")
	if pv == "" {
		pv = defaultPVersion
	}
	w.Header().Set("X-pVersion", pv)

	if p.cmd == "serverInfo" {
		s.serverInfo(w, r, p, pv)
		return
	}
	if p.cmd == "" {
		s.fail(w, r, errStatus(http.StatusBadRequest, "missing command"))
		return
	}
	cr, ok := s.reps[p.get("contRep")]
	if !ok {
		s.fail(w, r, errStatus(http.StatusNotFound, "unknown content repository %q", p.get("contRep")))
		return
	}
	var err error
	switch p.cmd {
	case "info":
		err = s.info(w, r, p, cr, pv)
	case "get":
		err = s.get(w, r, p, cr, pv)
	case "docGet":
		err = s.docGet(w, r, p, cr, pv)
	case "create":
		err = s.create(w, r, p, cr, pv)
	case "update":
		err = s.update(w, r, p, cr, pv)
	case "append":
		err = s.appendComp(w, r, p, cr, pv)
	case "delete":
		err = s.deleteDoc(w, r, p, cr, pv)
	case "search":
		err = s.search(w, r, p, cr, pv)
	case "putCert":
		err = s.putCert(w, r, p, cr)
	case "mCreate":
		err = s.mCreate(w, r, p, cr)
	case "attrSearch", "getCert":
		// attrSearch (print-list attribute index) and getCert (4.6 cache
		// server) are not implemented; see docs/content-server.md.
		err = errStatus(http.StatusNotImplemented, "%s is not supported by this content server", p.cmd)
	default:
		err = errStatus(http.StatusBadRequest, "unknown command %q", p.cmd)
	}
	if err != nil {
		s.fail(w, r, err)
	}
}

func (s *Server) allowedClient(r *http.Request) bool {
	if len(s.allowed) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	for _, n := range s.allowed {
		if ip != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

func requireMethod(r *http.Request, methods ...string) error {
	for _, m := range methods {
		if r.Method == m {
			return nil
		}
	}
	return errStatus(http.StatusMethodNotAllowed, "method %s not allowed for this command", r.Method)
}

func requireParam(p params, names ...string) error {
	for _, n := range names {
		if p.get(n) == "" {
			return errStatus(http.StatusBadRequest, "parameter %s is required", n)
		}
	}
	return nil
}
