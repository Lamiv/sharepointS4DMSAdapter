// Command adapter runs the SAP <-> SharePoint document adapter.
package main

import (
	"bytes"
	"context"
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
		log.Error("repository initialisation failed; retrying", "error", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}

	authn, err := auth.NewAuthenticator(cfg.Auth, &http.Client{Timeout: 15 * time.Second})
	if err != nil {
		return err
	}
	if cfg.Auth.Disabled {
		log.Warn("inbound authentication is DISABLED; do not use in production")
	}

	var ready atomic.Bool
	go readinessLoop(ctx, svc, &ready, log)

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
	adminMux := adminHandler(cfg.Server.EnablePprof, &ready)
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
	admin := newServer(cfg.Server, cfg.Server.AdminAddr, adminMux)
	admin.TLSConfig = nil

	errCh := make(chan error, len(servers)+1)
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
	useTLS := cfg.Server.TLSCertFile != "" && cfg.Server.TLSKeyFile != ""
	for _, s := range servers {
		go serve(s, useTLS)
	}
	go serve(admin, false)

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

func readinessLoop(ctx context.Context, svc *storage.Service, ready *atomic.Bool, log *slog.Logger) {
	check := func() {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := svc.Ping(cctx)
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

func adminHandler(enablePprof bool, ready *atomic.Bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
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
