package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/eavalenzuela/Moebius/shared/models"
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

func TestUserIDFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyUserID, "usr_abc")
	if got := UserIDFromContext(ctx); got != "usr_abc" {
		t.Errorf("UserIDFromContext = %q, want %q", got, "usr_abc")
	}
	if got := UserIDFromContext(context.Background()); got != "" {
		t.Errorf("UserIDFromContext(empty) = %q, want empty", got)
	}
}

func TestPermissionsFromContext(t *testing.T) {
	perms := []string{"devices:read", "jobs:create"}
	ctx := context.WithValue(context.Background(), ContextKeyPermissions, perms)
	got := PermissionsFromContext(ctx)
	if len(got) != 2 || got[0] != "devices:read" || got[1] != "jobs:create" {
		t.Errorf("PermissionsFromContext = %v, want %v", got, perms)
	}
	if got := PermissionsFromContext(context.Background()); got != nil {
		t.Errorf("PermissionsFromContext(empty) = %v, want nil", got)
	}
}

func TestScopeFromContext(t *testing.T) {
	scope := &models.APIScope{
		DeviceIDs: []string{"dev_1"},
	}
	ctx := context.WithValue(context.Background(), ContextKeyScope, scope)
	got := ScopeFromContext(ctx)
	if got == nil || len(got.DeviceIDs) != 1 {
		t.Errorf("ScopeFromContext = %v, want scope with 1 device", got)
	}
	if got := ScopeFromContext(context.Background()); got != nil {
		t.Errorf("ScopeFromContext(empty) = %v, want nil", got)
	}
}

func TestIsAdminFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyIsAdmin, true)
	if got := IsAdminFromContext(ctx); !got {
		t.Error("IsAdminFromContext = false, want true")
	}
	if got := IsAdminFromContext(context.Background()); got {
		t.Error("IsAdminFromContext(empty) = true, want false")
	}
}
