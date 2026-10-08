// Package graph is a focused Microsoft Graph client for SharePoint document
// libraries (drives). It handles authentication, retries with Retry-After,
// process-wide throttling back-off and a concurrency cap.
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"sharepointadapter/internal/auth"
	"sharepointadapter/internal/observability"
)

type Options struct {
	BaseURL         string
	MaxConcurrency  int
	MaxRetries      int
	MaxRetryBackoff time.Duration
	MaxIdleConns    int
}

type Client struct {
	base       string
	tokens     auth.TokenSource
	api        *http.Client // Graph API calls; follows redirects (Authorization is stripped cross-host)
	raw        *http.Client // pre-authenticated SharePoint URLs (upload sessions, downloads)
	sem        chan struct{}
	maxRetries int
	maxBackoff time.Duration
	log        *slog.Logger

	// throttledUntil holds a unix-nano deadline set when Graph answers 429/503
	// with Retry-After, so all callers back off together instead of piling on.
	throttledUntil atomic.Int64
}

// NewTransport returns an HTTP transport tuned for many concurrent,
// long-lived streaming connections to Graph/SharePoint.
func NewTransport(maxIdlePerHost int) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdlePerHost * 4,
		MaxIdleConnsPerHost:   maxIdlePerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		WriteBufferSize:       256 << 10,
		ReadBufferSize:        256 << 10,
	}
}

func New(opts Options, tokens auth.TokenSource, transport http.RoundTripper, log *slog.Logger) *Client {
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = 64
	}
	return &Client{
		base:       strings.TrimRight(opts.BaseURL, "/"),
		tokens:     tokens,
		api:        &http.Client{Transport: transport},
		raw:        &http.Client{Transport: transport},
		sem:        make(chan struct{}, opts.MaxConcurrency),
		maxRetries: opts.MaxRetries,
		maxBackoff: opts.MaxRetryBackoff,
		log:        log,
	}
}

// Error is a Graph (or SharePoint) error response.
type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("graph: %d %s: %s", e.Status, e.Code, e.Message)
}

func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }
func IsConflict(err error) bool { return statusIs(err, http.StatusConflict) }

func StatusOf(err error) int {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Status
	}
	return 0
}

func statusIs(err error, code int) bool { return StatusOf(err) == code }

// request describes a single logical Graph operation. body may be nil; when
// set it must be replayable (it is re-invoked on retry).
type request struct {
	op      string
	method  string
	url     string // absolute, or relative to base when starting with "/"
	body    func() (io.Reader, int64)
	header  http.Header
	noAuth  bool // pre-authenticated SharePoint URLs must not carry a token
	noRetry bool
}

// do executes req with retries and returns the response with an unread body
// on 2xx/3xx. Non-success statuses are converted to *Error.
func (c *Client) do(ctx context.Context, req request) (*http.Response, error) {
	target := req.url
	if strings.HasPrefix(target, "/") {
		target = c.base + target
	}
	hc := c.api
	if req.noAuth {
		hc = c.raw
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := c.waitThrottle(ctx); err != nil {
			return nil, err
		}
		if err := c.acquire(ctx); err != nil {
			return nil, err
		}
		resp, token, err := c.once(ctx, hc, target, req)
		c.release()

		reason := ""
		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr, reason = err, "network"
		case resp.StatusCode < 400:
			return resp, nil
		default:
			ge := readError(resp)
			lastErr = ge
			switch {
			case ge.Status == http.StatusUnauthorized && !req.noAuth && attempt == 0:
				c.tokens.Invalidate(token)
				reason = "auth"
			case ge.Status == http.StatusTooManyRequests || ge.Status == http.StatusServiceUnavailable:
				reason, wait = "throttled", ge.RetryAfter
				if wait > 0 {
					c.throttle(wait)
				}
			case ge.Status >= 500 && ge.Status != http.StatusNotImplemented:
				reason, wait = "server", ge.RetryAfter
			default:
				return nil, ge
			}
		}
		if req.noRetry || attempt >= c.maxRetries {
			return nil, lastErr
		}
		if wait <= 0 {
			wait = c.backoff(attempt)
		}
		observability.GraphRetries.WithLabelValues(req.op, reason).Inc()
		c.log.LogAttrs(ctx, slog.LevelWarn, "graph retry",
			slog.String("op", req.op), slog.String("reason", reason),
			slog.Int("attempt", attempt+1), slog.Duration("wait", wait),
			slog.String("request_id", observability.RequestID(ctx)),
			slog.String("error", lastErr.Error()))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func (c *Client) once(ctx context.Context, hc *http.Client, target string, req request) (*http.Response, string, error) {
	var body io.Reader
	var length int64 = -1
	if req.body != nil {
		body, length = req.body()
	}
	hr, err := http.NewRequestWithContext(ctx, req.method, target, body)
	if err != nil {
		return nil, "", err
	}
	if length >= 0 {
		hr.ContentLength = length
		if length == 0 {
			hr.Body = http.NoBody
		}
	}
	for k, v := range req.header {
		hr.Header[k] = v
	}
	if id := observability.RequestID(ctx); id != "" {
		hr.Header.Set("client-request-id", id)
	}
	var token string
	if !req.noAuth {
		if token, err = c.tokens.Token(ctx); err != nil {
			return nil, "", err
		}
		hr.Header.Set("Authorization", "Bearer "+token)
	}
	start := time.Now()
	resp, err := hc.Do(hr)
	observability.GraphDuration.WithLabelValues(req.op).Observe(time.Since(start).Seconds())
	code := "error"
	if resp != nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	observability.GraphRequests.WithLabelValues(req.op, code).Inc()
	return resp, token, err
}

func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	default:
	}
	start := time.Now()
	select {
	case c.sem <- struct{}{}:
		observability.GraphConcurrencyWait.Observe(time.Since(start).Seconds())
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() { <-c.sem }

func (c *Client) throttle(d time.Duration) {
	until := time.Now().Add(d).UnixNano()
	for {
		cur := c.throttledUntil.Load()
		if cur >= until || c.throttledUntil.CompareAndSwap(cur, until) {
			return
		}
	}
}

func (c *Client) waitThrottle(ctx context.Context) error {
	until := c.throttledUntil.Load()
	d := time.Until(time.Unix(0, until))
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	base := 200 * time.Millisecond << min(attempt, 8)
	if c.maxBackoff > 0 && base > c.maxBackoff {
		base = c.maxBackoff
	}
	// Full jitter avoids synchronized retry storms across clients.
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}

func readError(resp *http.Response) *Error {
	defer resp.Body.Close()
	ge := &Error{Status: resp.StatusCode, Code: http.StatusText(resp.StatusCode)}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			ge.RetryAfter = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			ge.RetryAfter = time.Until(t)
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
		ge.Code, ge.Message = env.Error.Code, env.Error.Message
	} else {
		ge.Message = strings.TrimSpace(string(raw))
	}
	return ge
}

func decode(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if v == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func jsonBody(v any) func() (io.Reader, int64) {
	b, _ := json.Marshal(v)
	return func() (io.Reader, int64) { return bytes.NewReader(b), int64(len(b)) }
}

func bytesBody(b []byte) func() (io.Reader, int64) {
	return func() (io.Reader, int64) { return bytes.NewReader(b), int64(len(b)) }
}

var jsonHeader = http.Header{"Content-Type": {"application/json"}}
