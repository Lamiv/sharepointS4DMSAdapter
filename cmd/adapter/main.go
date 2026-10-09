// Command adapter runs the SAP <-> SharePoint document adapter.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"sharepointadapter/internal/auth"
	"sharepointadapter/internal/config"
	"sharepointadapter/internal/contentrepo"
	"sharepointadapter/internal/graph"
	"sharepointadapter/internal/observability"
	"sharepointadapter/internal/restapi"
	"sharepointadapter/internal/storage"
	"sharepointadapter/internal/transfer"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", os.Getenv("CONFIG_FILE"), "path to YAML config")
	healthcheck := flag.Bool("healthcheck", false, "probe the local admin /healthz endpoint and exit (for Docker HEALTHCHECK)")
	flag.Parse()
	if *healthcheck {
		os.Exit(probe())
	}
	if flag.Arg(0) == "certs" {
		os.Exit(certsCLI(flag.Args()[1:]))
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(2)
	}
	log := observability.NewLogger(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(log)
	if err := run(cfg, log); err != nil {
		log.Error("adapter stopped", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	transport := graph.NewTransport(cfg.Graph.MaxIdleConns)
	tokenHTTP := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	tokens := auth.NewClientCredentials(cfg.Graph.AuthorityURL, cfg.Graph.TenantID, cfg.Graph.ClientID, cfg.Graph.ClientSecret, cfg.Graph.Scope, tokenHTTP)
	gc := graph.New(graph.Options{
		BaseURL:         cfg.Graph.BaseURL,
		MaxConcurrency:  cfg.Graph.MaxConcurrency,
		MaxRetries:      cfg.Graph.MaxRetries,
		MaxRetryBackoff: cfg.Graph.MaxRetryBackoff,
	}, tokens, transport, log)
	engine := transfer.New(gc, transfer.Options{
		SimpleUploadMax: cfg.Transfer.SimpleUploadMax,
		ChunkSize:       cfg.Transfer.ChunkSize,
		MemoryBudget:    cfg.Transfer.MemoryBudget,
		MaxUpload:       cfg.Transfer.MaxUpload,
		SpoolDir:        cfg.Transfer.SpoolDir,
	})

	// Health endpoints come up first so a failing start is diagnosable:
	// /readyz reports why the adapter is not ready instead of resetting
	// the connection.
	var ready atomic.Bool
	var status atomic.Value // string: current readiness reason
	status.Store("starting: resolving repositories")
	adminMux := adminHandler(cfg.Server.EnablePprof, &ready, &status)
	admin := newServer(cfg.Server, cfg.Server.AdminAddr, adminMux)
	admin.TLSConfig = nil
	errCh := make(chan error, 8)
	serve := func(s *http.Server, useTLS bool) {
		log.Info("listening", "addr", s.Addr, "tls", useTLS, "version", version)
		var err error
		if useTLS {
			err = s.ListenAndServeTLS(cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile)
		} else {
			err = s.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("%s: %w", s.Addr, err)
		}
	}
	go serve(admin, false)

	// Resolving site/library IDs needs Graph; retry so the container
	// survives starting before the network or IdP is reachable.
	var svc *storage.Service
	for attempt := 1; ; attempt++ {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		s, err := storage.NewService(rctx, gc, engine, cfg.Repositories, cfg.Graph.MetadataCacheTTL, log)
		cancel()
		if err == nil {
			svc = s
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		wait := min(time.Duration(attempt)*2*time.Second, 30*time.Second)
		hint := initHint(ctx, err, tokens)
		status.Store("not ready: repository initialisation failed: " + err.Error() + hint)
		log.Error("repository initialisation failed; retrying", "error", err, "hint", strings.TrimSpace(hint), "retry_in", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-time.After(wait):
		}
	}

	if err := contentrepo.ReserveFolders(svc, cfg.ContentServer); err != nil {
		return err
	}
	// Paging cursors must verify on every instance sharing these credentials.
	cursorKey := sha256.Sum256([]byte("sharepointadapter/cursor|" + cfg.Graph.TenantID + "|" + cfg.Graph.ClientID + "|" + cfg.Graph.ClientSecret))
	svc.SetCursorKey(cursorKey[:])

	authn, err := auth.NewAuthenticator(cfg.Auth, &http.Client{Timeout: 15 * time.Second})
	if err != nil {
		return err
	}
	if cfg.Auth.Disabled {
		log.Warn("inbound authentication is DISABLED; do not use in production")
	}

	go readinessLoop(ctx, svc, &ready, &status, log)

	var servers []*http.Server
	if cfg.Server.Enabled("rest") {
		api := restapi.New(svc, authn, restapi.Options{
			DownloadMode: cfg.Transfer.DownloadMode,
			MaxUpload:    cfg.Transfer.MaxUpload,
			CORSOrigins:  cfg.Server.CORSOrigins,
		}, log)
		servers = append(servers, newServer(cfg.Server, cfg.Server.RESTAddr,
			observability.Middleware("rest", cfg.Server.MaxInFlight, log, api.Handler())))
	}
	if cfg.Server.Enabled("contentrepo") {
		cs, err := contentrepo.New(ctx, svc, cfg.ContentServer, cfg.Graph.MetadataCacheTTL, version, log)
		if err != nil {
			return err
		}
		go cs.RefreshCertificates(ctx, time.Minute)
		cs.RegisterAdmin(adminMux, cfg.Server.AdminToken)
		servers = append(servers, newServer(cfg.Server, cfg.Server.ContentRepoAddr,
			observability.Middleware("contentrepo", cfg.Server.MaxInFlight, log, cs.Handler())))
	}
	if len(servers) == 0 {
		return errors.New("no interfaces enabled (server.interfaces)")
	}
	useTLS := cfg.Server.TLSCertFile != "" && cfg.Server.TLSKeyFile != ""
	for _, s := range servers {
		go serve(s, useTLS)
	}

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		return err
	}
	ready.Store(false)
	sctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range append(servers, admin) {
		wg.Add(1)
		go func(s *http.Server) {
			defer wg.Done()
			_ = s.Shutdown(sctx)
		}(s)
	}
	wg.Wait()
	return nil
}

func newServer(sc config.ServerConfig, addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: sc.ReadHeaderTO,
		IdleTimeout:       sc.IdleTimeout,
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		// No Read/WriteTimeout: large transfers may legitimately take minutes.
	}
}

func readinessLoop(ctx context.Context, svc *storage.Service, ready *atomic.Bool, status *atomic.Value, log *slog.Logger) {
	check := func() {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := svc.Ping(cctx)
		if err != nil {
			status.Store("not ready: SharePoint check failed: " + err.Error())
		} else {
			status.Store("ready")
		}
		if was := ready.Swap(err == nil); was != (err == nil) {
			if err != nil {
				log.Error("readiness check failed", "error", err)
			} else {
				log.Info("ready")
			}
		}
	}
	check()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

func adminHandler(enablePprof bool, ready *atomic.Bool, status *atomic.Value) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			reason, _ := status.Load().(string)
			http.Error(w, reason, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(version + "\n"))
	})
	if enablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	return mux
}

// probe is used as the container health check because the distroless image
// has no shell or curl.
func probe() int {
	u := os.Getenv("HEALTHCHECK_URL")
	if u == "" {
		u = "http://127.0.0.1:9090/healthz"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(u)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// certsCLI manages content server certificates through the local admin
// listener, e.g. inside the container:
//
//	/adapter certs list
//	/adapter certs activate Z1 CN=S4H
func certsCLI(args []string) int {
	base := os.Getenv("ADMIN_URL")
	if base == "" {
		base = "http://127.0.0.1:9090"
	}
	usage := func() int {
		fmt.Fprintln(os.Stderr, "usage: adapter certs list | activate <contRep> <authId> | deactivate <contRep> <authId>")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	var req *http.Request
	switch args[0] {
	case "list":
		req, _ = http.NewRequest(http.MethodGet, base+"/admin/contentserver/certificates", nil)
	case "activate", "deactivate":
		if len(args) != 3 {
			return usage()
		}
		body, _ := json.Marshal(map[string]string{"contRep": args[1], "authId": args[2]})
		req, _ = http.NewRequest(http.MethodPost, base+"/admin/contentserver/certificates/"+args[0], bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	default:
		return usage()
	}
	if t := os.Getenv("ADMIN_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(os.Stdout, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// initHint explains a failed repository lookup. A 401 from Graph although
// Entra issued a token almost always means the token carries no (or the
// wrong) application permissions, so the token's roles are shown.
func initHint(ctx context.Context, err error, tokens *auth.ClientCredentials) string {
	var ge *graph.Error
	if !errors.As(err, &ge) {
		return ""
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tok, terr := tokens.Token(tctx)
	if terr != nil {
		return " | the access token itself could not be obtained: " + terr.Error()
	}
	roles := auth.TokenRoles(tok)
	switch {
	case ge.Status == http.StatusUnauthorized && len(roles) == 0:
		return " | the access token has NO application permissions (roles): add Microsoft Graph APPLICATION permissions to the app registration and click 'Grant admin consent'"
	case ge.Status == http.StatusUnauthorized || ge.Status == http.StatusForbidden:
		return " | token roles: " + strings.Join(roles, ",") + " | these do not allow this lookup: siteUrl needs Sites.Read.All or Sites.ReadWrite.All; userId (OneDrive) needs Files.ReadWrite.All; also check that admin consent was granted"
	}
	return " | token roles: " + strings.Join(roles, ",")
}
