package auth

import (
	"net/http"
	"testing"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer sk_abc123", "sk_abc123"},
		{"Bearer ", ""},
		{"bearer sk_abc123", ""}, // case-sensitive
		{"Basic dXNlcjpwYXNz", ""},
		{"", ""},
	}
	for _, tt := range tests {
		r, _ := http.NewRequest("GET", "/", http.NoBody)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}
		got := extractBearerToken(r)
		if got != tt.want {
			t.Errorf("extractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestHashAPIKey_Deterministic(t *testing.T) {
	h1 := hashAPIKey("sk_test_key_123")
	h2 := hashAPIKey("sk_test_key_123")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
}

func TestHashAPIKey_DifferentInputs(t *testing.T) {
	h1 := hashAPIKey("sk_key_a")
	h2 := hashAPIKey("sk_key_b")
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}
