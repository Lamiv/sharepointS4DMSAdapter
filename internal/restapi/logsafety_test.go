package restapi_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lamiv/zaks-drive/internal/auth"
	"github.com/Lamiv/zaks-drive/internal/config"
	"github.com/Lamiv/zaks-drive/internal/graph"
	"github.com/Lamiv/zaks-drive/internal/graphmock"
	"github.com/Lamiv/zaks-drive/internal/observability"
	"github.com/Lamiv/zaks-drive/internal/restapi"
	"github.com/Lamiv/zaks-drive/internal/storage"
	"github.com/Lamiv/zaks-drive/internal/transfer"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// Synthetic sensitive values. None of them may ever appear in a log line.
const (
	secHeader = "SYNTH-HEADER-SECRET-1a2b3c"
	secKey    = "SYNTH-APIKEY-SECRET-4d5e6f"
	secBearer = "SYNTH-BEARER-SECRET-7a8b9c"
	secGraph  = "SYNTH-GRAPH-BODY-SECRET-d1e2f3"
	secURL    = "SYNTH-TEMPAUTH-SECRET-0a9b8c"
)

// hostile fronts the Graph emulator and misbehaves for chosen paths, the way a broken proxy or a
// compromised upstream could: it answers with error bodies and headers full of credential-shaped
// text, or drops the connection.
func hostile(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "LEAK501"):
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("request-id", "11111111-2222-3333-4444-555555555555")
			w.Header().Set("WWW-Authenticate", "Bearer "+secHeader)
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `{"error":{"code":"generalException","message":"failed for https://x.sharepoint.com/f?tempauth=`+secURL+` Authorization: Bearer `+secGraph+` client_secret=`+secGraph+`"}}`)
		case strings.Contains(r.URL.Path, "LEAK502"):
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>proxy error token="+secGraph+"</html>")
		case strings.Contains(r.URL.Path, "DROPCONN"):
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				_ = c.Close()
			}
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func setupLogged(t *testing.T, level slog.Level) (url string, logs *syncBuf, mockURL string) {
	t.Helper()
	logs = &syncBuf{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level}))
	mh, err := graphmock.New(graphmock.Options{DataDir: t.TempDir(), Drives: []string{"drive1"}})
	if err != nil {
		t.Fatal(err)
	}
	mock := httptest.NewServer(hostile(mh))
	t.Cleanup(mock.Close)
	tokens := auth.NewClientCredentials(mock.URL, "tenant", "client", "secret", "https://graph.microsoft.com/.default", mock.Client())
	gc := graph.New(graph.Options{BaseURL: mock.URL + "/v1.0", MaxConcurrency: 16, MaxRetries: 2, MaxRetryBackoff: 50 * time.Millisecond}, tokens, http.DefaultTransport, log)
	eng := transfer.New(gc, transfer.Options{SimpleUploadMax: 1 << 20, ChunkSize: 320 << 10, MemoryBudget: 8 << 20, MaxUpload: 50 << 20, SpoolDir: t.TempDir()})
	svc, err := storage.NewService(context.Background(), gc, eng, []config.RepositoryConfig{{ID: "DMS", DriveID: "drive1", RootPath: "SAP/DMS"}}, time.Minute, log)
	if err != nil {
		t.Fatal(err)
	}
	authn, err := auth.NewAuthenticator(config.AuthConfig{APIKeys: []config.APIKeyConfig{
		{Name: "rw", Key: "rw-key", Permissions: []string{"read", "write", "delete"}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api := restapi.New(svc, authn, restapi.Options{DownloadMode: "proxy", MaxUpload: 50 << 20}, log)
	srv := httptest.NewServer(observability.Middleware("rest", 100, log, api.Handler()))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/v1", logs, mock.URL
}

func call(t *testing.T, method, url string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Credentials sent by a caller, in every header shape, never reach a log or an error response.
func TestCallerCredentialsAreNeverLoggedOrEchoed(t *testing.T) {
	base, logs, _ := setupLogged(t, slog.LevelDebug)
	var bodies []string
	for _, h := range []map[string]string{
		{"Authorization": secHeader},                                           // no space: the "scheme" is the whole value
		{"Authorization": "Basic " + secHeader},                                // unsupported scheme
		{"Authorization": "Bearer " + secBearer},                               // bearer without JWT configured: tried as an API key
		{"Authorization": "ApiKey " + secKey},                                  // wrong API key
		{"X-API-Key": secKey},                                                  // wrong API key header
		{"Authorization": "Bearer eyJhbGciOiJub25lIn0." + secBearer + ".c2ln"}, // JWT-shaped
	} {
		code, body := call(t, "GET", base+"/repositories", h)
		if code != 401 {
			t.Fatalf("%v: status %d, want 401 (%s)", h, code, body)
		}
		bodies = append(bodies, body)
	}
	all := logs.String() + strings.Join(bodies, "\n")
	for _, s := range []string{secHeader, secKey, secBearer} {
		if strings.Contains(all, s) {
			t.Errorf("synthetic credential %q appears in a log or response:\n%s", s, all)
		}
	}
	if !strings.Contains(logs.String(), "AUTH_") {
		t.Errorf("refusals must still be logged with their code:\n%s", logs.String())
	}
}

// Text written by Microsoft Graph, or by whatever answers in its place, is not copied into logs.
func TestGraphErrorTextAndConnectionErrorsAreNotLogged(t *testing.T) {
	base, logs, mockURL := setupLogged(t, slog.LevelDebug)
	h := map[string]string{"X-API-Key": "rw-key"}
	var bodies []string
	for _, id := range []string{"LEAK501", "LEAK502", "DROPCONN"} {
		_, body := call(t, "GET", base+"/repositories/DMS/documents/"+id, h)
		bodies = append(bodies, body)
	}
	out := logs.String()
	for _, s := range []string{secHeader, secGraph, secURL, "tempauth"} {
		if strings.Contains(out, s) {
			t.Errorf("Graph-supplied text %q appears in the log:\n%s", s, out)
		}
	}
	if strings.Contains(out, mockURL) {
		t.Errorf("a full request URL appears in the log:\n%s", out)
	}
	// The same text must not come back to the REST caller unredacted either.
	for _, s := range []string{secGraph, secURL} {
		for _, b := range bodies {
			if strings.Contains(b, s) {
				t.Errorf("Graph-supplied secret %q is echoed to the caller: %s", s, b)
			}
		}
	}
	// Diagnostics stay useful: the request ID, application code and status are logged, and the
	// Graph request ID is available for correlation with Microsoft support.
	for _, want := range []string{"request_id=", "code=", "status=", "11111111-2222-3333-4444-555555555555"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}
