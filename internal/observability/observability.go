// Package observability provides structured logging, Prometheus metrics,
// request correlation and HTTP middleware shared by all adapter interfaces.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

func NewLogger(level, format string) *slog.Logger {
	var lv slog.Level
	_ = lv.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

var (
	durationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120}

	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_http_requests_total",
		Help: "Inbound HTTP requests by interface, route and status code.",
	}, []string{"interface", "route", "method", "code"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "adapter_http_request_duration_seconds",
		Help:    "Inbound HTTP request latency.",
		Buckets: durationBuckets,
	}, []string{"interface", "route", "method"})

	HTTPInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adapter_http_in_flight_requests",
		Help: "Inbound requests currently being served.",
	}, []string{"interface"})

	HTTPRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_http_rejected_total",
		Help: "Requests rejected by overload protection.",
	}, []string{"interface"})

	BytesTransferred = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_bytes_total",
		Help: "Document bytes transferred, by direction (upload|download).",
	}, []string{"direction"})

	GraphRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_graph_requests_total",
		Help: "Outbound Microsoft Graph requests by operation and status code.",
	}, []string{"op", "code"})

	GraphDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "adapter_graph_request_duration_seconds",
		Help:    "Outbound Microsoft Graph request latency (single attempt).",
		Buckets: durationBuckets,
	}, []string{"op"})

	GraphRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_graph_retries_total",
		Help: "Graph request retries by reason (throttled|server|network).",
	}, []string{"op", "reason"})

	GraphConcurrencyWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "adapter_graph_concurrency_wait_seconds",
		Help:    "Time spent waiting for a Graph concurrency slot.",
		Buckets: durationBuckets,
	})

	TokenRefreshes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_token_refresh_total",
		Help: "Graph access token acquisitions by result.",
	}, []string{"result"})

	TransferBufferWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "adapter_transfer_buffer_wait_seconds",
		Help:    "Time spent waiting for transfer memory budget.",
		Buckets: durationBuckets,
	})

	TransfersActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adapter_transfers_active",
		Help: "Uploads/downloads currently in progress.",
	}, []string{"direction", "mode"})
)

type ctxKey int

const requestIDKey ctxKey = 1

// RequestIDHeaders are inspected in order; SAP systems commonly send
// X-CorrelationID, browsers/proxies X-Request-ID.
var RequestIDHeaders = []string{"X-Request-ID", "X-CorrelationID", "X-Correlation-ID"}

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// statusRecorder captures status and response size without hiding optional
// interfaces that matter for streaming (Flush, ReadFrom via Unwrap).
type statusRecorder struct {
	http.ResponseWriter
	code  int
	bytes int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Middleware wraps a handler with request ID propagation, overload
// protection, metrics and access logging. The route label is the matched
// ServeMux pattern, which keeps metric cardinality low.
func Middleware(iface string, maxInFlight int, log *slog.Logger, next http.Handler) http.Handler {
	var inFlight atomic.Int64
	gauge := HTTPInFlight.WithLabelValues(iface)
	rejected := HTTPRejected.WithLabelValues(iface)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := ""
		for _, h := range RequestIDHeaders {
			if id = r.Header.Get(h); id != "" {
				break
			}
		}
		if id == "" || len(id) > 128 {
			id = newID()
		}
		w.Header().Set("X-Request-ID", id)

		if maxInFlight > 0 && inFlight.Add(1) > int64(maxInFlight) {
			inFlight.Add(-1)
			rejected.Inc()
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server busy", http.StatusServiceUnavailable)
			return
		} else if maxInFlight <= 0 {
			inFlight.Add(1)
		}
		gauge.Inc()
		defer func() {
			inFlight.Add(-1)
			gauge.Dec()
		}()

		rec := &statusRecorder{ResponseWriter: w}
		r = r.WithContext(WithRequestID(r.Context(), id))
		next.ServeHTTP(rec, r)

		if rec.code == 0 {
			rec.code = http.StatusOK
		}
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		HTTPRequests.WithLabelValues(iface, route, r.Method, strconv.Itoa(rec.code)).Inc()
		HTTPDuration.WithLabelValues(iface, route, r.Method).Observe(elapsed.Seconds())

		lvl := slog.LevelInfo
		if rec.code >= 500 {
			lvl = slog.LevelError
		} else if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			lvl = slog.LevelDebug
		}
		log.LogAttrs(r.Context(), lvl, "request",
			slog.String("iface", iface),
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.code),
			slog.Int64("bytes_out", rec.bytes),
			slog.Int64("bytes_in", r.ContentLength),
			slog.Duration("duration", elapsed),
			slog.String("remote", r.RemoteAddr),
		)
	})
}
