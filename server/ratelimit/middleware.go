package ratelimit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/metrics"
)

// PerIPMiddleware returns middleware that rate-limits by client IP address.
// It runs before authentication to shed unauthenticated abuse early.
func PerIPMiddleware(limiter *KeyedLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if allowed, retryAfter := limiter.Allow(ip); !allowed {
				metrics.RateLimitRejections.WithLabelValues("per_ip").Inc()
				slog.Warn("rate limit exceeded", slog.String("limiter", "per_ip"), slog.String("ip", ip)) //nolint:gosec // IP from RemoteAddr, not user input
				rejectRateLimited(w, r, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PerTenantMiddleware returns middleware that rate-limits by tenant ID.
// It must be placed after authentication middleware that sets the tenant context.
func PerTenantMiddleware(limiter *KeyedLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := auth.TenantIDFromContext(r.Context())
			if tenantID == "" {
				next.ServeHTTP(w, r)
				return
			}
			if allowed, retryAfter := limiter.Allow(tenantID); !allowed {
				metrics.RateLimitRejections.WithLabelValues("per_tenant").Inc()
				slog.Warn("rate limit exceeded", slog.String("limiter", "per_tenant"), slog.String("tenant_id", tenantID))
				rejectRateLimited(w, r, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PerAgentMiddleware returns middleware that rate-limits by agent/device ID.
// Intended for the check-in endpoint to prevent a single agent from flooding.
func PerAgentMiddleware(limiter *KeyedLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			agentID := auth.AgentIDFromContext(r.Context())
			if agentID == "" {
				next.ServeHTTP(w, r)
				return
			}
			if allowed, retryAfter := limiter.Allow(agentID); !allowed {
				metrics.RateLimitRejections.WithLabelValues("per_agent_checkin").Inc()
				slog.Warn("rate limit exceeded", slog.String("limiter", "per_agent_checkin"), slog.String("agent_id", agentID))
				rejectRateLimited(w, r, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func rejectRateLimited(w http.ResponseWriter, _ *http.Request, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)

	reqID := w.Header().Get("X-Request-ID")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":       "rate_limited",
			"message":    fmt.Sprintf("Too many requests. Try again in %d seconds.", secs),
			"request_id": reqID,
		},
	})
}
