//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/eavalenzuela/Moebius/server/rbac"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// 20.8 — RBAC enforcement

func TestRBAC_ViewerCanReadButNotWrite(t *testing.T) {
	h := newHarness(t)

	// Enroll a device so there's data to read
	h.enrollAgent("rbac-host")

	// Create a viewer key
	viewerKey := h.createAPIKeyWithPerms("viewer", rbac.ViewerPermissions, false)

	// Viewer can list devices
	resp := h.apiRequestWithKey(viewerKey, "GET", "/v1/devices", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Viewer can list jobs
	resp = h.apiRequestWithKey(viewerKey, "GET", "/v1/jobs", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Viewer cannot create jobs
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo forbidden"})
	resp = h.apiRequestWithKey(viewerKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{"dev_doesntmatter"}},
		"payload": json.RawMessage(payload),
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer creating job: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Viewer cannot create enrollment tokens
	resp = h.apiRequestWithKey(viewerKey, "POST", "/v1/enrollment-tokens", map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer creating enrollment token: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Viewer cannot manage users
	resp = h.apiRequestWithKey(viewerKey, "GET", "/v1/users", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer reading users: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Viewer cannot manage roles
	resp = h.apiRequestWithKey(viewerKey, "GET", "/v1/roles", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer reading roles: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRBAC_TechnicianCanCreateJobsButNotManageUsers(t *testing.T) {
	h := newHarness(t)

	agentID, _, _ := h.enrollAgent("tech-rbac-host")

	techKey := h.createAPIKeyWithPerms("technician", rbac.TechnicianPermissions, false)

	// Technician can read devices
	resp := h.apiRequestWithKey(techKey, "GET", "/v1/devices", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Technician can create jobs
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo tech"})
	resp = h.apiRequestWithKey(techKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Technician cannot manage users
	resp = h.apiRequestWithKey(techKey, "GET", "/v1/users", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("technician reading users: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Technician cannot manage API keys
	resp = h.apiRequestWithKey(techKey, "GET", "/v1/api-keys", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("technician reading api keys: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Technician cannot write devices
	resp = h.apiRequestWithKey(techKey, "PATCH", "/v1/devices/"+agentID, map[string]any{
		"hostname": "new-name",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("technician writing device: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Technician cannot revoke devices
	resp = h.apiRequestWithKey(techKey, "POST", "/v1/devices/"+agentID+"/revoke", map[string]any{
		"reason": "test",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("technician revoking device: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRBAC_OperatorCanManageMostButNotUsersOrRoles(t *testing.T) {
	h := newHarness(t)

	agentID, _, _ := h.enrollAgent("operator-rbac-host")

	operatorKey := h.createAPIKeyWithPerms("operator", rbac.OperatorPermissions, false)

	// Operator can read + write devices
	resp := h.apiRequestWithKey(operatorKey, "GET", "/v1/devices", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = h.apiRequestWithKey(operatorKey, "PATCH", "/v1/devices/"+agentID, map[string]any{
		"hostname": "renamed-host",
	})
	// Should succeed (operator has devices:write)
	if resp.StatusCode == http.StatusForbidden {
		t.Error("operator should be able to write devices")
	}
	resp.Body.Close()

	// Operator cannot manage users
	resp = h.apiRequestWithKey(operatorKey, "GET", "/v1/users", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("operator reading users: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Operator cannot manage roles
	resp = h.apiRequestWithKey(operatorKey, "GET", "/v1/roles", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("operator reading roles: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRBAC_TenantAdminHasFullTenantAccess(t *testing.T) {
	h := newHarness(t)

	// Tenant Admin is permission-based, not is_admin=true. It should be able
	// to reach every tenant-scoped endpoint via the canonical permission set
	// declared in server/rbac/permissions.go.
	tenantAdminKey := h.createAPIKeyWithPerms("tenant-admin", rbac.TenantAdminPermissions, false)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/devices"},
		{"GET", "/v1/jobs"},
		{"GET", "/v1/users"},
		{"GET", "/v1/roles"},
		{"GET", "/v1/api-keys"},
		{"GET", "/v1/groups"},
		{"GET", "/v1/tags"},
		{"GET", "/v1/sites"},
		{"GET", "/v1/tenant"},
		{"GET", "/v1/audit-log"},
		{"GET", "/v1/signing-keys"},
		{"GET", "/v1/alert-rules"},
		{"GET", "/v1/scheduled-jobs"},
		{"GET", "/v1/agent-versions"},
		{"GET", "/v1/enrollment-tokens"},
	}

	for _, ep := range endpoints {
		resp := h.apiRequestWithKey(tenantAdminKey, ep.method, ep.path, nil)
		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("tenant admin got 403 on %s %s", ep.method, ep.path)
		}
		resp.Body.Close()
	}

	// Tenant admin can mint enrollment tokens (a write op)
	resp := h.apiRequestWithKey(tenantAdminKey, "POST", "/v1/enrollment-tokens", map[string]any{})
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("tenant admin should be able to create enrollment tokens, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRBAC_AdminBypassesAll(t *testing.T) {
	h := newHarness(t)

	// The admin key created in the harness has is_admin=true

	// Admin can access everything
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/devices"},
		{"GET", "/v1/jobs"},
		{"GET", "/v1/users"},
		{"GET", "/v1/roles"},
		{"GET", "/v1/api-keys"},
		{"GET", "/v1/groups"},
		{"GET", "/v1/tags"},
		{"GET", "/v1/sites"},
		{"GET", "/v1/tenant"},
		{"GET", "/v1/audit-log"},
		{"GET", "/v1/signing-keys"},
		{"GET", "/v1/alert-rules"},
		{"GET", "/v1/scheduled-jobs"},
		{"GET", "/v1/agent-versions"},
		{"GET", "/v1/enrollment-tokens"},
	}

	for _, ep := range endpoints {
		resp := h.apiRequestWithKey(h.adminKey, ep.method, ep.path, nil)
		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("admin got 403 on %s %s", ep.method, ep.path)
		}
		resp.Body.Close()
	}
}

func TestRBAC_NoKeyReturns401(t *testing.T) {
	h := newHarness(t)

	req, _ := http.NewRequest("GET", h.apiURL+"/v1/devices", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no auth: expected 401, got %d", resp.StatusCode)
	}
}

func TestRBAC_InvalidKeyReturns401(t *testing.T) {
	h := newHarness(t)

	resp := h.apiRequestWithKey("sk_not_a_real_key", "GET", "/v1/devices", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("invalid key: expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRBAC_CustomRoleWithPartialPerms(t *testing.T) {
	h := newHarness(t)

	// Create a custom role with only devices:read + groups:read
	customKey := h.createAPIKeyWithPerms("custom-partial", []string{"devices:read", "groups:read"}, false)

	// Can read devices
	resp := h.apiRequestWithKey(customKey, "GET", "/v1/devices", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Can read groups
	resp = h.apiRequestWithKey(customKey, "GET", "/v1/groups", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Cannot read jobs (no permission)
	resp = h.apiRequestWithKey(customKey, "GET", "/v1/jobs", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("custom role reading jobs: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Cannot read tags (no permission)
	resp = h.apiRequestWithKey(customKey, "GET", "/v1/tags", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("custom role reading tags: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
