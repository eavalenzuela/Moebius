package api

import (
	"encoding/json"
	"net/http"

	"github.com/eavalenzuela/Moebius/server/quota"
)

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// APIError is the standard error response per REST_API_SPEC.md.
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// Error writes a standard JSON error response.
func Error(w http.ResponseWriter, status int, msg string) {
	reqID := w.Header().Get("X-Request-ID")
	JSON(w, status, map[string]any{
		"error": APIError{
			Code:      httpCodeToErrorCode(status),
			Message:   msg,
			RequestID: reqID,
		},
	})
}

// ErrorWithCode writes a JSON error response with a specific error code.
func ErrorWithCode(w http.ResponseWriter, status int, code, msg string) {
	reqID := w.Header().Get("X-Request-ID")
	JSON(w, status, map[string]any{
		"error": APIError{
			Code:      code,
			Message:   msg,
			RequestID: reqID,
		},
	})
}

// HandleQuotaError renders a 409 quota_exceeded response when err is a
// quota.ErrExceeded and returns true. Returns false for non-quota
// errors so the caller can fall through to its 500 path.
func HandleQuotaError(w http.ResponseWriter, err error) bool {
	if qe, ok := quota.AsExceeded(err); ok {
		ErrorWithCode(w, http.StatusConflict, "quota_exceeded", qe.Error())
		return true
	}
	return false
}

func httpCodeToErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return "internal_error"
	}
}

// Pagination is the pagination envelope for list responses.
type Pagination struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
	Limit      int    `json:"limit"`
}

// ListResponse wraps a list of items with pagination.
type ListResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}
