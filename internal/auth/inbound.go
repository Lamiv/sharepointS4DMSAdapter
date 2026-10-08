// Package auth contains inbound caller authentication and the outbound
// Microsoft Graph token provider.
package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"sharepointadapter/internal/config"
)

type Permission string

const (
	PermRead   Permission = "read"
	PermWrite  Permission = "write"
	PermDelete Permission = "delete"
)

// Principal is an authenticated caller.
type Principal struct {
	Name         string
	Repositories []string // empty = all
	Permissions  []Permission
}

func (p *Principal) Can(repo string, perm Permission) bool {
	if p == nil {
		return false
	}
	if !slices.Contains(p.Permissions, perm) {
		return false
	}
	return len(p.Repositories) == 0 || slices.Contains(p.Repositories, "*") || slices.Contains(p.Repositories, repo)
}

var ErrUnauthenticated = errors.New("unauthenticated")

type Authenticator struct {
	disabled bool
	keys     map[string]*Principal // sha256 hex -> principal
	jwt      *jwtVerifier
}

func NewAuthenticator(cfg config.AuthConfig, hc *http.Client) (*Authenticator, error) {
	a := &Authenticator{disabled: cfg.Disabled, keys: map[string]*Principal{}}
	for _, k := range cfg.APIKeys {
		h := strings.ToLower(k.SHA256)
		if k.Key != "" {
			sum := sha256.Sum256([]byte(k.Key))
			h = hex.EncodeToString(sum[:])
		}
		if len(h) != 64 {
			return nil, fmt.Errorf("api key %q: provide key or 64-char sha256", k.Name)
		}
		perms := parsePerms(k.Permissions)
		if len(perms) == 0 {
			perms = []Permission{PermRead}
		}
		a.keys[h] = &Principal{Name: "apikey:" + k.Name, Repositories: k.Repositories, Permissions: perms}
	}
	if cfg.JWT != nil {
		a.jwt = &jwtVerifier{cfg: *cfg.JWT, http: hc, keys: map[string]*rsa.PublicKey{}}
	}
	return a, nil
}

// Authenticate resolves the caller from X-API-Key or an Authorization header.
func (a *Authenticator) Authenticate(r *http.Request) (*Principal, error) {
	if a.disabled {
		return &Principal{Name: "anonymous", Permissions: []Permission{PermRead, PermWrite, PermDelete}}, nil
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		return a.byKey(key)
	}
	authz := r.Header.Get("Authorization")
	scheme, cred, _ := strings.Cut(authz, " ")
	switch {
	case strings.EqualFold(scheme, "ApiKey"):
		return a.byKey(cred)
	case strings.EqualFold(scheme, "Bearer") && a.jwt != nil:
		return a.jwt.verify(r.Context(), cred)
	case strings.EqualFold(scheme, "Bearer"):
		// Allow API keys as bearer tokens when JWT is not configured.
		return a.byKey(cred)
	}
	return nil, ErrUnauthenticated
}

func (a *Authenticator) byKey(key string) (*Principal, error) {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	if p, ok := a.keys[hex.EncodeToString(sum[:])]; ok {
		return p, nil
	}
	return nil, ErrUnauthenticated
}

func parsePerms(in []string) []Permission {
	var out []Permission
	for _, s := range in {
		switch p := Permission(strings.ToLower(strings.TrimSpace(s))); p {
		case PermRead, PermWrite, PermDelete:
			out = append(out, p)
		}
	}
	return out
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// ---- JWT (OIDC access tokens, e.g. Entra ID or SAP IAS) ----

type jwtVerifier struct {
	cfg  config.JWTConfig
	http *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

func (v *jwtVerifier) verify(ctx context.Context, raw string) (*Principal, error) {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "PS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(60 * time.Second),
	}
	if v.cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.cfg.Issuer))
	}
	if v.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(v.cfg.Audience))
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.key(ctx, kid)
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	name, _ := claims["sub"].(string)
	if n, ok := claims["preferred_username"].(string); ok && n != "" {
		name = n
	} else if n, ok := claims["appid"].(string); ok && n != "" {
		name = n
	}
	rolesClaim := v.cfg.RolesClaim
	if rolesClaim == "" {
		rolesClaim = "roles"
	}
	var roles []string
	switch rv := claims[rolesClaim].(type) {
	case []any:
		for _, x := range rv {
			if s, ok := x.(string); ok {
				roles = append(roles, s)
			}
		}
	case string:
		roles = strings.Fields(rv)
	}
	for i, r := range roles {
		roles[i] = strings.TrimPrefix(r, v.cfg.RolePrefix)
	}
	return &Principal{Name: "jwt:" + name, Permissions: parsePerms(roles)}, nil
}

func (v *jwtVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	k, ok := v.keys[kid]
	stale := time.Since(v.fetchedAt) > time.Hour
	recent := time.Since(v.fetchedAt) < 30*time.Second
	v.mu.RUnlock()
	if ok && !stale {
		return k, nil
	}
	if !ok && recent {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	if err := v.refresh(ctx); err != nil && !ok {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown key id %q", kid)
}

func (v *jwtVerifier) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks: status %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}
