package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
)

func TestPerIPMiddleware_Allows(t *testing.T) {
	limiter := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 5}, time.Minute)
	handler := PerIPMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestPerIPMiddleware_Blocks(t *testing.T) {
	limiter := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 1}, time.Minute)
	handler := PerIPMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "192.168.1.1:12345"

	// First request succeeds
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got %d", rr.Code)
	}

	// Second request should be rate-limited
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}

	// Check Retry-After header
	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}

	// Check JSON body
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("response missing error object")
	}
	if errObj["code"] != "rate_limited" {
		t.Errorf("expected code 'rate_limited', got %v", errObj["code"])
	}
}

func TestPerTenantMiddleware_Allows(t *testing.T) {
	limiter := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 5}, time.Minute)
	handler := PerTenantMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	ctx := context.WithValue(req.Context(), auth.ContextKeyTenantID, "ten_123")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestPerTenantMiddleware_Blocks(t *testing.T) {
	limiter := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 1}, time.Minute)
	handler := PerTenantMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	ctx := context.WithValue(req.Context(), auth.ContextKeyTenantID, "ten_123")
	req = req.WithContext(ctx)

	// Exhaust
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should block
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestPerTenantMiddleware_NoTenantPassesThrough(t *testing.T) {
	limiter := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 1}, time.Minute)
	called := false
	handler := PerTenantMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	// No tenant in context
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should be called when no tenant in context")
	}
}

func TestPerAgentMiddleware_Blocks(t *testing.T) {
	limiter := NewKeyedLimiter(BucketConfig{Rate: 10, Burst: 1}, time.Minute)
	handler := PerAgentMiddleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/checkin", http.NoBody)
	ctx := context.WithValue(req.Context(), auth.ContextKeyAgentID, "dev_abc")
	req = req.WithContext(ctx)

	// Exhaust
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should block
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestClientIP_WithPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "10.0.0.1:54321"
	if got := clientIP(req); got != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", got)
	}
}

func TestClientIP_IPv6(t *testing.T) {
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "[::1]:8080"
	if got := clientIP(req); got != "::1" {
		t.Errorf("expected ::1, got %s", got)
	}
}

func TestClientIP_NoPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "10.0.0.1"
	if got := clientIP(req); got != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", got)
	}
}
