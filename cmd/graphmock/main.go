// Command graphmock runs the Microsoft Graph emulator for development and
// load testing.
//
// Env:
//
//	MOCK_ADDR            listen address (default :8000)
//	MOCK_DATA_DIR        blob directory (default temp dir)
//	MOCK_LATENCY_MS      added latency per API call, e.g. "80" or "50-150"
//	MOCK_THROTTLE_RATE   fraction of API calls answered 429 (e.g. 0.01)
//	MOCK_DRIVES          comma-separated drive IDs (default "drive1")
//	MOCK_RETAIN_BYTES    cap on stored content; later uploads are size-checked but discarded
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sharepointadapter/internal/graphmock"
)

func main() {
	addr := env("MOCK_ADDR", ":8000")
	o := graphmock.Options{DataDir: env("MOCK_DATA_DIR", filepath.Join(os.TempDir(), "graphmock"))}
	o.LatencyMin, o.LatencyMax = parseLatency(os.Getenv("MOCK_LATENCY_MS"))
	o.ThrottleRate, _ = strconv.ParseFloat(os.Getenv("MOCK_THROTTLE_RATE"), 64)
	o.RetainBytes, _ = strconv.ParseInt(os.Getenv("MOCK_RETAIN_BYTES"), 10, 64)
	for _, d := range strings.Split(env("MOCK_DRIVES", "drive1"), ",") {
		o.Drives = append(o.Drives, strings.TrimSpace(d))
	}
	h, err := graphmock.New(o)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("graphmock listening on %s (latency %v-%v, throttle %.3f, data %s)", addr, o.LatencyMin, o.LatencyMax, o.ThrottleRate, o.DataDir)
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseLatency(v string) (time.Duration, time.Duration) {
	if v == "" {
		return 0, 0
	}
	lo, hi, found := strings.Cut(v, "-")
	a, _ := strconv.Atoi(lo)
	b := a
	if found {
		b, _ = strconv.Atoi(hi)
	}
	return time.Duration(a) * time.Millisecond, time.Duration(b) * time.Millisecond
}
