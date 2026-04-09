package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/eavalenzuela/Moebius/server/metrics"
)

type contextKey string

const (
	// ContextKeyRequestID is the context key for the request ID.
	ContextKeyRequestID contextKey = "request_id"
)

// RequestIDFromContext extracts the request ID from the context.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyRequestID).(string)
	return v
}

// RequestID is middleware that ensures every request has an X-Request-ID.
// If the client provides one it's echoed back; otherwise one is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), ContextKeyRequestID, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func generateRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "req_" + hex.EncodeToString(b)
}

// MetricsMiddleware records request duration for the Prometheus histogram.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		duration := time.Since(start).Seconds()
		metrics.APIRequestDurationSeconds.WithLabelValues(
			r.Method, r.URL.Path, strconv.Itoa(sw.status),
		).Observe(duration)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Body-size limits (DoS protection). The constants below cap how much
// memory a single request can force the server to allocate before any
// handler logic runs.
//
// chi composes middleware outside-in, so when a route is wrapped by
// multiple MaxBytes layers the innermost (most restrictive) wins. The
// global root cap is therefore set to the largest legitimate body any
// endpoint accepts; per-route overrides clamp it down on the few
// endpoints that need to be even tighter than the global default.
const (
	// MaxBodyBytesGlobal is the absolute upper bound for any HTTP request
	// body the server accepts. Sized just above the largest legitimate
	// body (~5 MB chunk upload + HTTP overhead).
	MaxBodyBytesGlobal int64 = 8 * 1024 * 1024 // 8 MB

	// MaxBodyBytesJSON caps ordinary JSON CRUD endpoints when applied
	// per-route. Big enough for bulk inserts but tight enough that a
	// caller cannot exhaust memory with a single absurd JSON payload.
	MaxBodyBytesJSON int64 = 1 * 1024 * 1024 // 1 MB

	// MaxBodyBytesAgentInventory caps an agent check-in payload. Inventory
	// lists (installed packages, hardware) can be large; 4 MB is generous
	// for tens of thousands of entries while still bounding worst-case.
	MaxBodyBytesAgentInventory int64 = 4 * 1024 * 1024 // 4 MB

	// MaxBodyBytesAgentLogs caps a single agent log shipment.
	MaxBodyBytesAgentLogs int64 = 4 * 1024 * 1024 // 4 MB

	// MaxBodyBytesFileChunk caps a single chunk PUT. Must accommodate the
	// 5 MB defaultChunkSize plus a small slack for HTTP overhead.
	MaxBodyBytesFileChunk int64 = 6 * 1024 * 1024 // 6 MB
)

// MaxBytes returns a middleware that caps r.Body at n bytes via
// http.MaxBytesReader. Reads beyond n yield 413 Payload Too Large from
// the underlying reader; handlers that decode JSON will surface the
// limit as a decode error and return 400 — that's acceptable. The
// middleware itself does not pre-emptively check Content-Length so it
// catches both honest oversized clients and chunked-encoding attackers.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ParsePagination extracts limit and cursor from query parameters.
// Returns validated limit (default 50, max 500) and cursor string.
func ParsePagination(r *http.Request) (limit int, cursor string) {
	cursor = r.URL.Query().Get("cursor")

	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	return limit, cursor
}
