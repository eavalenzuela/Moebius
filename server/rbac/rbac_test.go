package rbac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eavalenzuela/Moebius/server/auth"
)

func TestRequire_Allowed(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyPermissions, []string{"devices:read", "jobs:read"})

	handler := Require("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", http.NoBody).WithContext(ctx)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequire_Forbidden(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyPermissions, []string{"devices:read"})

	handler := Require("users:write")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", http.NoBody).WithContext(ctx)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequire_AdminBypass(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyIsAdmin, true)
	// No permissions set — admin should bypass

	handler := Require("tenant:write")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", http.NoBody).WithContext(ctx)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequire_NoPermissions(t *testing.T) {
	handler := Require("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", http.NoBody)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestHasPermission(t *testing.T) {
	perms := []string{"devices:read", "jobs:create", "inventory:read"}

	if !hasPermission(perms, "devices:read") {
		t.Error("expected devices:read to be found")
	}
	if hasPermission(perms, "users:write") {
		t.Error("expected users:write to not be found")
	}
	if hasPermission(nil, "devices:read") {
		t.Error("expected nil perms to return false")
	}
}
