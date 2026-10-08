// Package config loads adapter configuration from a YAML file with ${ENV}
// expansion, then applies well-known environment overrides for secrets.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Graph        GraphConfig        `yaml:"graph"`
	Transfer     TransferConfig     `yaml:"transfer"`
	Auth         AuthConfig         `yaml:"auth"`
	Repositories []RepositoryConfig `yaml:"repositories"`
	// ContentServer configures the SAP Content Server HTTP interface.
	ContentServer ContentServerConfig `yaml:"contentServer"`
	Log           LogConfig           `yaml:"log"`
}

type ContentServerConfig struct {
	// AllowedNetworks restricts callers to these CIDRs (empty = any).
	AllowedNetworks []string `yaml:"allowedNetworks"`
	// ClockSkew tolerated when checking the signed URL expiration.
	ClockSkew time.Duration `yaml:"clockSkew"`
	// AutoActivateCertificates activates certificates received via putCert
	// immediately (trust on first use). Keep false in production.
	AutoActivateCertificates bool                      `yaml:"autoActivateCertificates"`
	Repositories             []ContentRepositoryConfig `yaml:"repositories"`
}

type ContentRepositoryConfig struct {
	// ContRep is the SAP content repository ID from transaction OAC0.
	ContRep     string `yaml:"contRep"`
	Description string `yaml:"description"`
	// Repository references a storage repository (library + root path).
	Repository string `yaml:"repository"`
	// Folder below the repository root; defaults to "ContentServer/<contRep>".
	Folder string `yaml:"folder"`
	// Signature: required (default) | optional | none. Must match OAC0.
	Signature string `yaml:"signature"`
}

type ServerConfig struct {
	// Interfaces to start: "rest", "contentrepo". Admin (metrics/health) always runs.
	Interfaces      []string      `yaml:"interfaces"`
	RESTAddr        string        `yaml:"restAddr"`
	ContentRepoAddr string        `yaml:"contentRepoAddr"`
	AdminAddr       string        `yaml:"adminAddr"`
	MaxInFlight     int           `yaml:"maxInFlight"` // per listener; excess requests get 503
	ReadHeaderTO    time.Duration `yaml:"readHeaderTimeout"`
	IdleTimeout     time.Duration `yaml:"idleTimeout"`
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
	CORSOrigins     []string      `yaml:"corsOrigins"`
	TLSCertFile     string        `yaml:"tlsCertFile"`
	TLSKeyFile      string        `yaml:"tlsKeyFile"`
	EnablePprof     bool          `yaml:"enablePprof"`
	// AdminToken, when set, is required (Bearer) for mutating admin
	// endpoints such as certificate activation.
	AdminToken string `yaml:"adminToken"`
}

type GraphConfig struct {
	TenantID     string `yaml:"tenantId"`
	ClientID     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
	// AuthorityURL defaults to https://login.microsoftonline.com.
	AuthorityURL string `yaml:"authorityUrl"`
	// BaseURL defaults to https://graph.microsoft.com/v1.0.
	BaseURL string `yaml:"baseUrl"`
	Scope   string `yaml:"scope"`
	// MaxConcurrency caps simultaneous Graph calls across the process to
	// stay below SharePoint throttling thresholds.
	MaxConcurrency  int           `yaml:"maxConcurrency"`
	MaxRetries      int           `yaml:"maxRetries"`
	MaxIdleConns    int           `yaml:"maxIdleConnsPerHost"`
	MaxRetryBackoff time.Duration `yaml:"maxRetryBackoff"`
	// MetadataCacheTTL caches item metadata (incl. pre-authenticated
	// download URLs, valid ~1h) to make repeated reads one hop. 0 disables.
	MetadataCacheTTL time.Duration `yaml:"metadataCacheTtl"`
}

type TransferConfig struct {
	// Files at or below this size use a single PUT; larger use an upload session.
	SimpleUploadMax int64 `yaml:"simpleUploadMaxBytes"`
	// Chunk size for upload sessions; rounded down to a multiple of 320 KiB.
	ChunkSize int64 `yaml:"chunkSizeBytes"`
	// MemoryBudget bounds the total bytes held in transfer buffers process-wide.
	MemoryBudget int64  `yaml:"memoryBudgetBytes"`
	MaxUpload    int64  `yaml:"maxUploadBytes"`
	SpoolDir     string `yaml:"spoolDir"`
	// DownloadMode: "proxy" streams bytes through the adapter, "redirect"
	// returns a 302 to the short-lived pre-authenticated SharePoint URL.
	DownloadMode string `yaml:"downloadMode"`
}

type AuthConfig struct {
	APIKeys []APIKeyConfig `yaml:"apiKeys"`
	JWT     *JWTConfig     `yaml:"jwt"`
	// Disabled turns off inbound auth entirely (local dev only).
	Disabled bool `yaml:"disabled"`
}

type APIKeyConfig struct {
	Name string `yaml:"name"`
	// SHA256 is the hex-encoded SHA-256 of the key. Key holds a plaintext key
	// (allowed for convenience, e.g. injected via ${ENV}).
	SHA256       string   `yaml:"sha256"`
	Key          string   `yaml:"key"`
	Repositories []string `yaml:"repositories"` // empty or "*" = all
	Permissions  []string `yaml:"permissions"`  // read, write, delete
}

type JWTConfig struct {
	Issuer   string `yaml:"issuer"`
	Audience string `yaml:"audience"`
	JWKSURL  string `yaml:"jwksUrl"`
	// RolesClaim holds permission strings, e.g. "roles" (Entra) or "scope".
	RolesClaim string `yaml:"rolesClaim"`
	// RolePrefix is stripped from role values: "Documents.Read" -> "read".
	RolePrefix string `yaml:"rolePrefix"`
}

type RepositoryConfig struct {
	ID string `yaml:"id"`
	// One of DriveID, SiteID (+DriveName) or SiteURL identifies the library.
	DriveID   string `yaml:"driveId"`
	SiteID    string `yaml:"siteId"`
	SiteURL   string `yaml:"siteUrl"`
	DriveName string `yaml:"driveName"`
	RootPath  string `yaml:"rootPath"`
	ReadOnly  bool   `yaml:"readOnly"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // json | text
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Interfaces:      []string{"rest"},
			RESTAddr:        ":8080",
			ContentRepoAddr: ":8090",
			AdminAddr:       ":9090",
			MaxInFlight:     1000,
			ReadHeaderTO:    10 * time.Second,
			IdleTimeout:     120 * time.Second,
			ShutdownTimeout: 30 * time.Second,
		},
		Graph: GraphConfig{
			AuthorityURL:     "https://login.microsoftonline.com",
			BaseURL:          "https://graph.microsoft.com/v1.0",
			Scope:            "https://graph.microsoft.com/.default",
			MaxConcurrency:   64,
			MaxRetries:       5,
			MaxIdleConns:     256,
			MaxRetryBackoff:  60 * time.Second,
			MetadataCacheTTL: 60 * time.Second,
		},
		Transfer: TransferConfig{
			SimpleUploadMax: 4 << 20,
			ChunkSize:       5 * 1024 * 1024, // 16 x 320 KiB
			MemoryBudget:    512 << 20,
			MaxUpload:       2 << 30,
			SpoolDir:        os.TempDir(),
			DownloadMode:    "proxy",
		},
		Log: LogConfig{Level: "info", Format: "json"},
	}
}

// Load reads the YAML file at path (optional; "" skips) and applies env overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		expanded := os.ExpandEnv(string(raw))
		if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(&cfg)
	normalize(&cfg)
	return cfg, cfg.Validate()
}

func applyEnv(c *Config) {
	str := func(name string, dst *string) {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			*dst = v
		}
	}
	str("ADMIN_TOKEN", &c.Server.AdminToken)
	str("GRAPH_TENANT_ID", &c.Graph.TenantID)
	str("GRAPH_CLIENT_ID", &c.Graph.ClientID)
	str("GRAPH_CLIENT_SECRET", &c.Graph.ClientSecret)
	str("GRAPH_AUTHORITY_URL", &c.Graph.AuthorityURL)
	str("GRAPH_BASE_URL", &c.Graph.BaseURL)
	str("LOG_LEVEL", &c.Log.Level)
	str("DOWNLOAD_MODE", &c.Transfer.DownloadMode)
	if v := os.Getenv("ADAPTER_INTERFACES"); v != "" {
		c.Server.Interfaces = splitList(v)
	}
	if v := os.Getenv("GRAPH_MAX_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Graph.MaxConcurrency = n
		}
	}
	if v := os.Getenv("METADATA_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Graph.MetadataCacheTTL = d
		}
	}
	if os.Getenv("AUTH_DISABLED") == "true" {
		c.Auth.Disabled = true
	}
}

func normalize(c *Config) {
	const frag = 320 * 1024
	if c.Transfer.ChunkSize < frag {
		c.Transfer.ChunkSize = frag
	}
	c.Transfer.ChunkSize -= c.Transfer.ChunkSize % frag
	if c.Transfer.MemoryBudget < c.Transfer.ChunkSize {
		c.Transfer.MemoryBudget = c.Transfer.ChunkSize
	}
	if c.Transfer.SimpleUploadMax > c.Transfer.MemoryBudget {
		c.Transfer.SimpleUploadMax = c.Transfer.MemoryBudget
	}
	if c.ContentServer.ClockSkew == 0 {
		c.ContentServer.ClockSkew = 5 * time.Minute
	}
	for i := range c.ContentServer.Repositories {
		r := &c.ContentServer.Repositories[i]
		if r.Folder == "" {
			r.Folder = "ContentServer/" + r.ContRep
		}
		r.Folder = strings.Trim(r.Folder, "/")
		if r.Signature == "" {
			r.Signature = "required"
		}
	}
	c.Graph.BaseURL = strings.TrimRight(c.Graph.BaseURL, "/")
	c.Graph.AuthorityURL = strings.TrimRight(c.Graph.AuthorityURL, "/")
	for i := range c.Repositories {
		c.Repositories[i].RootPath = strings.Trim(c.Repositories[i].RootPath, "/")
	}
}

func (c Config) Validate() error {
	var errs []error
	if c.Graph.TenantID == "" || c.Graph.ClientID == "" || c.Graph.ClientSecret == "" {
		errs = append(errs, errors.New("graph.tenantId, graph.clientId and graph.clientSecret are required"))
	}
	if len(c.Repositories) == 0 {
		errs = append(errs, errors.New("at least one repository must be configured"))
	}
	seen := map[string]bool{}
	for _, r := range c.Repositories {
		if r.ID == "" {
			errs = append(errs, errors.New("repository id is required"))
		}
		if seen[r.ID] {
			errs = append(errs, fmt.Errorf("duplicate repository id %q", r.ID))
		}
		seen[r.ID] = true
		if r.DriveID == "" && r.SiteID == "" && r.SiteURL == "" {
			errs = append(errs, fmt.Errorf("repository %q: one of driveId, siteId or siteUrl is required", r.ID))
		}
	}
	if c.Server.Enabled("contentrepo") && len(c.ContentServer.Repositories) == 0 {
		errs = append(errs, errors.New("contentServer.repositories is required when the contentrepo interface is enabled"))
	}
	seenRep := map[string]bool{}
	for _, cr := range c.ContentServer.Repositories {
		if cr.ContRep == "" || len(cr.ContRep) > 2 {
			errs = append(errs, fmt.Errorf("contentServer: contRep %q must be 1-2 characters (OAC0 repository ID)", cr.ContRep))
		}
		if seenRep[cr.ContRep] {
			errs = append(errs, fmt.Errorf("contentServer: duplicate contRep %q", cr.ContRep))
		}
		seenRep[cr.ContRep] = true
		if !seen[cr.Repository] {
			errs = append(errs, fmt.Errorf("contentServer: contRep %q references unknown repository %q", cr.ContRep, cr.Repository))
		}
		switch cr.Signature {
		case "required", "optional", "none":
		default:
			errs = append(errs, fmt.Errorf("contentServer: contRep %q signature must be required, optional or none", cr.ContRep))
		}
	}
	if c.Transfer.DownloadMode != "proxy" && c.Transfer.DownloadMode != "redirect" {
		errs = append(errs, fmt.Errorf("transfer.downloadMode must be proxy or redirect"))
	}
	if c.Server.Enabled("rest") && !c.Auth.Disabled && len(c.Auth.APIKeys) == 0 && c.Auth.JWT == nil {
		errs = append(errs, errors.New("auth: configure apiKeys and/or jwt, or set auth.disabled=true"))
	}
	return errors.Join(errs...)
}

func (s ServerConfig) Enabled(name string) bool {
	for _, i := range s.Interfaces {
		if strings.EqualFold(i, name) {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
