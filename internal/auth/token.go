package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"sharepointadapter/internal/observability"
)

// TokenSource returns a bearer token for Microsoft Graph.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	// Invalidate drops a cached token, e.g. after Graph returned 401.
	Invalidate(token string)
}

// ClientCredentials implements the OAuth 2.0 client credentials grant
// against Entra ID. Tokens are cached and refreshed ahead of expiry; a
// singleflight group guarantees one in-flight token request under load.
type ClientCredentials struct {
	tokenURL     string
	clientID     string
	clientSecret string
	scope        string
	http         *http.Client

	mu      sync.RWMutex
	token   string
	expires time.Time
	group   singleflight.Group
	now     func() time.Time
}

const refreshSkew = 5 * time.Minute

func NewClientCredentials(authority, tenant, clientID, secret, scope string, hc *http.Client) *ClientCredentials {
	return &ClientCredentials{
		tokenURL:     fmt.Sprintf("%s/%s/oauth2/v2.0/token", authority, url.PathEscape(tenant)),
		clientID:     clientID,
		clientSecret: secret,
		scope:        scope,
		http:         hc,
		now:          time.Now,
	}
}

func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	c.mu.RLock()
	tok, exp := c.token, c.expires
	c.mu.RUnlock()
	if tok != "" && c.now().Before(exp.Add(-refreshSkew)) {
		return tok, nil
	}
	// Detach from the caller's context so one cancelled request does not
	// fail the shared fetch for every waiter.
	ch := c.group.DoChan("token", func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return c.fetch(fctx)
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			// Still-valid token is better than failing outright during an IdP blip.
			if tok != "" && c.now().Before(exp) {
				return tok, nil
			}
			return "", res.Err
		}
		return res.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (c *ClientCredentials) Invalidate(token string) {
	c.mu.Lock()
	if c.token == token {
		c.token = ""
	}
	c.mu.Unlock()
}

func (c *ClientCredentials) fetch(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {c.scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		observability.TokenRefreshes.WithLabelValues("error").Inc()
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		observability.TokenRefreshes.WithLabelValues("error").Inc()
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		return "", fmt.Errorf("token endpoint returned %d: %s %s", resp.StatusCode, e.Error, firstLine(e.Description))
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		observability.TokenRefreshes.WithLabelValues("error").Inc()
		return "", fmt.Errorf("invalid token response")
	}
	if t.ExpiresIn <= 0 {
		t.ExpiresIn = 3600
	}
	c.mu.Lock()
	c.token = t.AccessToken
	c.expires = c.now().Add(time.Duration(t.ExpiresIn) * time.Second)
	c.mu.Unlock()
	observability.TokenRefreshes.WithLabelValues("ok").Inc()
	return t.AccessToken, nil
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// TokenRoles returns the application roles (the "roles" claim) of an
// access token without verifying it. It is used only for diagnostics: a
// token with no roles is rejected by Graph with 401, which is otherwise
// hard to tell apart from a bad credential.
func TokenRoles(token string) []string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims struct {
		Roles []string `json:"roles"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims.Roles
}
