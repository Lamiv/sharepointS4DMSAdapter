package graph_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Lamiv/zaks-drive/internal/apperr"
	"github.com/Lamiv/zaks-drive/internal/auth"
	"github.com/Lamiv/zaks-drive/internal/graph"
	"github.com/Lamiv/zaks-drive/internal/graphmock"
)

func newClient(t *testing.T) *graph.Client {
	t.Helper()
	mh, err := graphmock.New(graphmock.Options{DataDir: t.TempDir(), Drives: []string{"drive1"}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mh)
	t.Cleanup(srv.Close)
	tokens := auth.NewClientCredentials(srv.URL, "tenant", "client", "secret", "scope", srv.Client())
	return graph.New(graph.Options{BaseURL: srv.URL + "/v1.0", MaxConcurrency: 4, MaxRetries: 1, MaxRetryBackoff: 20 * time.Millisecond},
		tokens, http.DefaultTransport, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// OneDrive for Business is addressed with userId (/users/{upn}/drive).
func TestResolveDriveOfAUser(t *testing.T) {
	c := newClient(t)
	id, err := c.ResolveDrive(context.Background(), "jane@contoso.com", "", "", "")
	if err != nil || id != "drive1" {
		t.Fatalf("user drive: %q %v", id, err)
	}
	_, err = c.ResolveDrive(context.Background(), "missing@contoso.com", "", "", "")
	if ae, ok := apperr.As(err); !ok || ae.Code != "SP_USER_DRIVE_NOT_FOUND" {
		t.Fatalf("user without OneDrive: %v", err)
	}
}

func TestResolveDriveProblemsAreCoded(t *testing.T) {
	c := newClient(t)
	// the emulator knows no sites: Graph answers 404
	_, err := c.ResolveDrive(context.Background(), "", "", "https://contoso.sharepoint.com/sites/none", "")
	if ae, ok := apperr.As(err); !ok || ae.Code != "SP_SITE_NOT_FOUND" {
		t.Fatalf("unknown site: %v", err)
	}
	_, err = c.ResolveDrive(context.Background(), "", "", "not a url", "")
	if ae, ok := apperr.As(err); !ok || ae.Code != "CFG_SITE_URL_INVALID" {
		t.Fatalf("bad site url: %v", err)
	}
}
