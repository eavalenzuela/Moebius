package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Checker is a dependency that can be pinged for readiness.
type Checker interface {
	Ping(ctx context.Context) error
}

// Handler provides HTTP handlers for liveness and readiness probes.
type Handler struct {
	checks []namedCheck
}

type namedCheck struct {
	name    string
	checker Checker
}

// New creates a Handler with the given dependency checkers.
func New(checks map[string]Checker) *Handler {
	h := &Handler{}
	for name, c := range checks {
		h.checks = append(h.checks, namedCheck{name: name, checker: c})
	}
	return h
}

// Liveness returns 200 if the process is running. GET /health
func (h *Handler) Liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Readiness checks all dependencies and returns 200 only if all are healthy.
// GET /health/ready
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	result := make(map[string]string, len(h.checks))
	allOK := true

	for _, c := range h.checks {
		if err := c.checker.Ping(ctx); err != nil {
			result[c.name] = err.Error()
			allOK = false
		} else {
			result[c.name] = "ok"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if allOK {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(result)
}
