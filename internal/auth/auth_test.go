package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sharepointadapter/internal/config"
)

func TestTokenCachedAndSingleflight(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "s" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"abc","expires_in":3600}`))
	}))
	defer srv.Close()
	cc := NewClientCredentials(srv.URL, "tenant", "id", "s", "scope", srv.Client())
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := cc.Token(context.Background()); err != nil || tok != "abc" {
				t.Errorf("token %q %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("token endpoint called %d times", calls.Load())
	}
	cc.Invalidate("abc")
	_, _ = cc.Token(context.Background())
	if calls.Load() != 2 {
		t.Fatalf("expected refetch after invalidate, calls=%d", calls.Load())
	}
}

func TestAPIKeys(t *testing.T) {
	a, err := NewAuthenticator(config.AuthConfig{APIKeys: []config.APIKeyConfig{
		{Name: "a", Key: "k1", Repositories: []string{"DMS"}, Permissions: []string{"read", "write"}},
		// pre-hashed key (value irrelevant for this test)
		{Name: "b", SHA256: "e8d6ae9e1aa59b8f6e5f0bb0ff4c9e4b5c6cdc2e1e6a0bbd5e1c32c3d7fc5f5a"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "ApiKey k1")
	p, err := a.Authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Can("DMS", PermWrite) || p.Can("DMS", PermDelete) || p.Can("OTHER", PermRead) {
		t.Fatalf("unexpected permissions %+v", p)
	}
	r.Header.Set("Authorization", "Bearer nope")
	if _, err := a.Authenticate(r); err == nil {
		t.Fatal("expected failure")
	}
}
