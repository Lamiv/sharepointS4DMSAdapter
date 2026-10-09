package accesscheck

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sharepointadapter/internal/auth"
	"sharepointadapter/internal/config"
	"sharepointadapter/internal/graph"
	"sharepointadapter/internal/graphmock"
)

func newEnv(t *testing.T) (*graph.Client, auth.TokenSource, string) {
	t.Helper()
	mh, err := graphmock.New(graphmock.Options{DataDir: t.TempDir(), Drives: []string{"drive1"}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mh)
	t.Cleanup(srv.Close)
	tokens := auth.NewClientCredentials(srv.URL, "tenant", "client", "secret", "scope", srv.Client())
	gc := graph.New(graph.Options{BaseURL: srv.URL + "/v1.0", MaxConcurrency: 8, MaxRetries: 1, MaxRetryBackoff: 50 * time.Millisecond},
		tokens, http.DefaultTransport, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return gc, tokens, srv.URL
}

func cfgFor(repos ...config.RepositoryConfig) config.Config {
	var c config.Config
	c.Graph.ClientSecret = "secret"
	c.Repositories = repos
	return c
}

func TestAllChecksPass(t *testing.T) {
	gc, tokens, _ := newEnv(t)
	var out bytes.Buffer
	code := Run(context.Background(), cfgFor(config.RepositoryConfig{ID: "DMS", DriveID: "drive1", RootPath: "SAP_DMS"}), gc, tokens, &out)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	for _, want := range []string{"[PASS] Application permission in the token", "[PASS] [DMS] Write: upload a small file",
		"[PASS] [DMS] Read: download and compare", "[PASS] [DMS] Delete the test file", "passed 10, failed 0, skipped 0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "secret") && strings.Contains(out.String(), "secret length") == false {
		t.Error("secret may have leaked into the report")
	}
}

func TestMissingLibraryFailsAndSkipsDependents(t *testing.T) {
	gc, tokens, _ := newEnv(t)
	var out bytes.Buffer
	code := Run(context.Background(), cfgFor(config.RepositoryConfig{ID: "DMS", DriveID: "nope", RootPath: "SAP_DMS"}), gc, tokens, &out)
	// A drive ID is taken as given, so the first real access (write) fails.
	if code != 1 || !strings.Contains(out.String(), "[FAIL] [DMS] Write: upload a small file") ||
		!strings.Contains(out.String(), "[SKIP] [DMS] Read: download and compare") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "fix     : not found:") {
		t.Errorf("expected a fix hint:\n%s", out.String())
	}
}

type noRoles struct{ auth.TokenSource }

func (noRoles) Token(context.Context) (string, error) {
	b := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return "mock-" + b("{}") + "." + b(`{"aud":"graph"}`) + ".sig", nil
}

func TestTokenWithoutRolesIsReported(t *testing.T) {
	gc, tokens, _ := newEnv(t)
	var out bytes.Buffer
	code := Run(context.Background(), cfgFor(config.RepositoryConfig{ID: "DMS", DriveID: "drive1"}), gc, noRoles{tokens}, &out)
	if code != 1 || !strings.Contains(out.String(), "[FAIL] Application permission in the token") ||
		!strings.Contains(out.String(), "roles in token: NONE") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}

func TestReadOnlyRepositorySkipsWrites(t *testing.T) {
	gc, tokens, _ := newEnv(t)
	var out bytes.Buffer
	code := Run(context.Background(), cfgFor(config.RepositoryConfig{ID: "RO", DriveID: "drive1", ReadOnly: true}), gc, tokens, &out)
	if code != 0 || !strings.Contains(out.String(), "[SKIP] [RO] Write: upload a small file") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
}
