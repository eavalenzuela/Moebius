package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestID_Generated(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := RequestIDFromContext(r.Context())
		if reqID == "" {
			t.Error("expected request ID in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", http.NoBody)
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header")
	}
}

func TestRequestID_Echoed(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.Header.Set("X-Request-ID", "my-custom-id")
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("X-Request-ID") != "my-custom-id" {
		t.Errorf("X-Request-ID = %q, want %q", rr.Header().Get("X-Request-ID"), "my-custom-id")
	}
}

func TestParsePagination_Defaults(t *testing.T) {
	req := httptest.NewRequest("GET", "/", http.NoBody)
	limit, cursor := ParsePagination(req)

	if limit != 50 {
		t.Errorf("limit = %d, want 50", limit)
	}
	if cursor != "" {
		t.Errorf("cursor = %q, want empty", cursor)
	}
}

func TestParsePagination_Custom(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=100&cursor=abc123", http.NoBody)
	limit, cursor := ParsePagination(req)

	if limit != 100 {
		t.Errorf("limit = %d, want 100", limit)
	}
	if cursor != "abc123" {
		t.Errorf("cursor = %q, want %q", cursor, "abc123")
	}
}

func TestParsePagination_MaxLimit(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=1000", http.NoBody)
	limit, _ := ParsePagination(req)

	if limit != 500 {
		t.Errorf("limit = %d, want 500 (clamped)", limit)
	}
}

func TestParsePagination_InvalidLimit(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=abc", http.NoBody)
	limit, _ := ParsePagination(req)

	if limit != 50 {
		t.Errorf("limit = %d, want 50 (default)", limit)
	}
}

func TestError_Format(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Request-ID", "req_test123")
	Error(rr, http.StatusNotFound, "Device not found")

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	body := rr.Body.String()
	if body == "" {
		t.Fatal("empty body")
	}
	// Verify it contains the expected fields
	for _, want := range []string{`"code"`, `"not_found"`, `"message"`, `"Device not found"`, `"request_id"`, `"req_test123"`} {
		if !contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
