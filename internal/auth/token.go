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

	"github.com/Lamiv/zaks-drive/internal/apperr"
	"github.com/Lamiv/zaks-drive/internal/observability"
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
		// Cause is set: a network problem may be transient, so callers retry.
		return "", apperr.New(apperr.TokenFailed, "the request to Microsoft Entra ID (%s) failed: %v", c.tokenHost(), err).
			WithFix("Allow outbound HTTPS (443) from this server to login.microsoftonline.com; check DNS, proxy and the system clock.").Wrap(err)
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
		desc := firstLine(e.Description)
		return "", apperr.New(apperr.TokenFailed, "Microsoft Entra ID answered HTTP %d: %s %s", resp.StatusCode, e.Error, desc).
			WithFix("%s", entraFix(resp.StatusCode, e.Error, desc))
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		observability.TokenRefreshes.WithLabelValues("error").Inc()
		return "", apperr.New(apperr.TokenFailed, "Microsoft Entra ID answered HTTP 200 without an access token").
			WithFix("Check graph.authorityUrl / GRAPH_AUTHORITY_URL: it should be https://login.microsoftonline.com")
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

func (c *ClientCredentials) tokenHost() string {
	if u, err := url.Parse(c.tokenURL); err == nil {
		return u.Host
	}
	return "login.microsoftonline.com"
}

// entraFix maps Microsoft Entra ID's error to the usual correction.
func entraFix(status int, code, desc string) string {
	d := strings.ToUpper(desc)
	switch {
	case strings.Contains(d, "AADSTS7000222"):
		return "The client secret has EXPIRED. Create a new secret in Entra (App registration > Certificates & secrets), put its Value in GRAPH_CLIENT_SECRET in deploy/.env and recreate the container."
	case strings.Contains(d, "AADSTS7000215") || code == "invalid_client":
		return "The client secret is wrong. Use the secret VALUE (not its Secret ID) in GRAPH_CLIENT_SECRET in deploy/.env; check for stray spaces or quotes, then recreate the container."
	case strings.Contains(d, "AADSTS700016") || code == "unauthorized_client":
		return "No application with this client ID exists in this tenant. Check GRAPH_CLIENT_ID (Application (client) ID) and GRAPH_TENANT_ID in deploy/.env."
	case strings.Contains(d, "AADSTS90002") || strings.Contains(d, "AADSTS900023") || code == "invalid_tenant":
		return "The tenant ID is wrong. GRAPH_TENANT_ID must be the Directory (tenant) ID (a GUID) or the tenant's domain."
	case strings.Contains(d, "AADSTS1002012") || strings.Contains(d, "AADSTS70011") || code == "invalid_scope":
		return "The requested scope is invalid. graph.scope should be https://graph.microsoft.com/.default."
	case status >= 500:
		return "Microsoft Entra ID has a problem; retry later and check https://status.cloud.microsoft ."
	}
	return "Run `zaks-drive check`. The message above is Microsoft's own explanation: look up its AADSTS code at https://login.microsoftonline.com/error ."
}
