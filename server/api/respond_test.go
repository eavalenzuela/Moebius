package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	JSON(rr, http.StatusOK, map[string]string{"hello": "world"})

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var got map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("body hello = %q, want %q", got["hello"], "world")
	}
}

func TestJSON_StatusCreated(t *testing.T) {
	rr := httptest.NewRecorder()
	JSON(rr, http.StatusCreated, map[string]int{"id": 1})

	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusCreated)
	}
}

func TestErrorWithCode(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Request-ID", "req_abc")
	ErrorWithCode(rr, http.StatusConflict, "duplicate_name", "Name already exists")

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusConflict)
	}

	var resp map[string]APIError
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	apiErr := resp["error"]
	if apiErr.Code != "duplicate_name" {
		t.Errorf("code = %q, want %q", apiErr.Code, "duplicate_name")
	}
	if apiErr.Message != "Name already exists" {
		t.Errorf("message = %q, want %q", apiErr.Message, "Name already exists")
	}
	if apiErr.RequestID != "req_abc" {
		t.Errorf("request_id = %q, want %q", apiErr.RequestID, "req_abc")
	}
}

func TestErrorWithCode_NoRequestID(t *testing.T) {
	rr := httptest.NewRecorder()
	ErrorWithCode(rr, http.StatusBadRequest, "bad_request", "invalid")

	var resp map[string]APIError
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"].RequestID != "" {
		t.Errorf("request_id = %q, want empty", resp["error"].RequestID)
	}
}

func TestHttpCodeToErrorCode(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "bad_request"},
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not_found"},
		{http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.StatusConflict, "conflict"},
		{http.StatusTooManyRequests, "rate_limited"},
		{http.StatusInternalServerError, "internal_error"},
		{http.StatusServiceUnavailable, "internal_error"}, // unmapped → default
	}
	for _, tt := range tests {
		got := httpCodeToErrorCode(tt.status)
		if got != tt.want {
			t.Errorf("httpCodeToErrorCode(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
