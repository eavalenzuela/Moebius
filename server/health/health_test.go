package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockChecker struct {
	err error
}

func (m *mockChecker) Ping(_ context.Context) error { return m.err }

func TestLiveness(t *testing.T) {
	h := New(nil)
	rr := httptest.NewRecorder()
	h.Liveness(rr, httptest.NewRequest("GET", "/health", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("body status = %q, want %q", body["status"], "ok")
	}
}

func TestReadiness_AllHealthy(t *testing.T) {
	h := New(map[string]Checker{
		"db":    &mockChecker{},
		"cache": &mockChecker{},
	})
	rr := httptest.NewRecorder()
	h.Readiness(rr, httptest.NewRequest("GET", "/health/ready", http.NoBody))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestReadiness_OneUnhealthy(t *testing.T) {
	h := New(map[string]Checker{
		"db":    &mockChecker{},
		"cache": &mockChecker{err: errors.New("connection refused")},
	})
	rr := httptest.NewRecorder()
	h.Readiness(rr, httptest.NewRequest("GET", "/health/ready", http.NoBody))

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var body map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["db"] != "ok" {
		t.Errorf("db = %q, want %q", body["db"], "ok")
	}
	if body["cache"] != "connection refused" {
		t.Errorf("cache = %q, want %q", body["cache"], "connection refused")
	}
}
