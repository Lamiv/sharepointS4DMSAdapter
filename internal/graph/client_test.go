package graph

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type staticTokens struct{ invalidated atomic.Int32 }

func (s *staticTokens) Token(context.Context) (string, error) { return "t", nil }
func (s *staticTokens) Invalidate(string)                     { s.invalidated.Add(1) }

func newTestClient(url string, tokens *staticTokens) *Client {
	return New(Options{BaseURL: url, MaxConcurrency: 4, MaxRetries: 3, MaxRetryBackoff: 50 * time.Millisecond},
		tokens, http.DefaultTransport, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRetriesThrottlingThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":"activityLimitReached","message":"slow down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","name":"a.txt","size":3}`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL, &staticTokens{})
	it, err := c.GetItem(context.Background(), "d", "x")
	if err != nil || it.Name != "a.txt" {
		t.Fatalf("got %v %v", it, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestNoRetryOnClientErrorAndTokenInvalidation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"itemNotFound","message":"nope"}}`))
	}))
	defer srv.Close()
	tokens := &staticTokens{}
	c := newTestClient(srv.URL, tokens)
	_, err := c.GetItem(context.Background(), "d", "x")
	if !IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
	if calls.Load() != 2 || tokens.invalidated.Load() != 1 {
		t.Fatalf("calls=%d invalidated=%d", calls.Load(), tokens.invalidated.Load())
	}
}

func TestConcurrencyCap(t *testing.T) {
	var cur, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL, &staticTokens{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.GetItem(context.Background(), "d", "x")
		}()
	}
	wg.Wait()
	if peak.Load() > 4 {
		t.Fatalf("peak concurrency %d exceeds cap 4", peak.Load())
	}
}

func TestItemPath(t *testing.T) {
	it := &DriveItem{Name: "a b.pdf"}
	it.ParentReference = &struct {
		DriveID string `json:"driveId"`
		ID      string `json:"id"`
		Path    string `json:"path"`
	}{Path: "/drives/x/root:/SAP/DMS%20Docs"}
	if got := it.Path(); got != "/SAP/DMS Docs/a b.pdf" {
		t.Fatalf("path %q", got)
	}
	if EscapePath("/a b/c#d/") != "a%20b/c%23d" {
		t.Fatalf("escape %q", EscapePath("/a b/c#d/"))
	}
}

// Real Graph drops "@microsoft.graph.downloadUrl" when it is part of a
// $select field list, so metadata reads must not send $select.
func TestMetadataReadsSendNoSelect(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"id":"x","value":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL, &staticTokens{})
	_, _ = c.GetItem(context.Background(), "d", "x")
	_, _ = c.GetItemByPath(context.Background(), "d", "a/b")
	_, _ = c.ListChildren(context.Background(), "d", "a", 50, "")
	_, _ = c.Search(context.Background(), "d", "a", "q", 50, "")
	for _, q := range seen {
		if strings.Contains(q, "select") {
			t.Errorf("request used $select: %q", q)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("requests: %v", seen)
	}
}
