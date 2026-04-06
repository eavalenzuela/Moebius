//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// ─── 2.1 Authentication & Session Management ─────────────

// TestSecurity_ExpiredAPIKeyRejected verifies that an API key with an expiry
// in the past is rejected on every request.
func TestSecurity_ExpiredAPIKeyRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create an API key that expired an hour ago
	rawKey := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	keyHash := hashKey(rawKey)
	keyID := models.NewAPIKeyID()
	expired := time.Now().Add(-1 * time.Hour)

	_, err := h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		keyID, h.tenantID, h.userID, "expired-key", keyHash, h.roleID, false, expired, time.Now())
	if err != nil {
		t.Fatalf("create expired key: %v", err)
	}

	resp := h.apiRequestWithKey(rawKey, "GET", "/v1/devices", nil)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)
}

// TestSecurity_APIKeyHashNotInResponse verifies that API key list responses
// never contain the key_hash field (json:"-" tag enforcement).
func TestSecurity_APIKeyHashNotInResponse(t *testing.T) {
	h := newHarness(t)

	resp := h.apiRequest("GET", "/v1/api-keys", nil)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)

	var raw map[string]json.RawMessage
	readJSON(t, resp, &raw)

	// Check that no key_hash appears in the raw JSON
	rawBytes, _ := json.Marshal(raw)
	if containsStr(string(rawBytes), "key_hash") {
		t.Error("API key list response contains 'key_hash' field — should be excluded via json:\"-\" tag")
	}
}

// TestSecurity_EnrollmentTokenExpiry verifies that an expired enrollment
// token is rejected even if it has never been used.
func TestSecurity_EnrollmentTokenExpiry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create a token that expired 1 hour ago
	enrollment := auth.NewEnrollmentService(h.pool)
	// We can't create an already-expired token via the service, so insert directly
	rawToken := hex.EncodeToString(mustRandBytes(t, 32))
	tokenHash := sha256Hex(rawToken)
	tokenID := models.NewEnrollmentTokenID()

	_, err := h.pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (id, tenant_id, token_hash, created_by, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		tokenID, h.tenantID, tokenHash, h.userID, time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("insert expired token: %v", err)
	}

	// Attempt to consume it
	_, err = enrollment.ValidateAndConsume(ctx, rawToken)
	if err == nil {
		t.Fatal("expected expired token to be rejected, but it was consumed")
	}
}

// TestSecurity_EnrollmentTokenRaceConcurrent fires N concurrent enrollments
// with the same token and asserts exactly one succeeds (atomicity guarantee).
func TestSecurity_EnrollmentTokenRaceConcurrent(t *testing.T) {
	h := newHarness(t)

	enrollment := auth.NewEnrollmentService(h.pool)
	result, err := enrollment.CreateToken(context.Background(), h.tenantID, h.userID, nil, 24*time.Hour)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	successes := make(chan int, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := enrollment.ValidateAndConsume(context.Background(), result.Raw)
			if err == nil {
				successes <- 1
			}
		}()
	}

	wg.Wait()
	close(successes)

	count := 0
	for range successes {
		count++
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 successful consume, got %d", count)
	}
}

// ─── 2.2 Authorization (RBAC + Scope) ────────────────────

// TestSecurity_ScopedKeyCannotAccessOutOfScopeDevice creates a scoped API key
// that only has access to a specific device, then verifies it cannot read a
// different device.
func TestSecurity_ScopedKeyCannotAccessOutOfScopeDevice(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Enroll two agents (need mTLS for this)
	h.startMTLSServer()
	agent1ID, _, _ := h.enrollAgent("scope-test-host-1")
	agent2ID, _, _ := h.enrollAgent("scope-test-host-2")

	// Create a scoped API key that only covers agent1
	scope := &models.APIScope{DeviceIDs: []string{agent1ID}}
	scopeJSON, _ := json.Marshal(scope)

	roleID := models.NewRoleID()
	permsJSON, _ := json.Marshal([]string{"devices:read", "jobs:read", "jobs:create"})
	_, err := h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom) VALUES ($1, $2, $3, $4, $5)`,
		roleID, h.tenantID, "scoped-role", permsJSON, true)
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	rawKey := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	keyHash := hashKey(rawKey)
	keyID := models.NewAPIKeyID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, scope, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		keyID, h.tenantID, h.userID, "scoped-key", keyHash, roleID, scopeJSON, false, time.Now())
	if err != nil {
		t.Fatalf("create scoped key: %v", err)
	}

	// Scoped key CAN read agent1
	resp1 := h.apiRequestWithKey(rawKey, "GET", "/v1/devices/"+agent1ID, nil)
	resp1.Body.Close()
	if resp1.StatusCode == http.StatusForbidden || resp1.StatusCode == http.StatusNotFound {
		t.Errorf("scoped key should be able to read in-scope device, got %d", resp1.StatusCode)
	}

	// Scoped key CANNOT read agent2 (should get 404 to avoid existence leaks)
	resp2 := h.apiRequestWithKey(rawKey, "GET", "/v1/devices/"+agent2ID, nil)
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Errorf("scoped key should NOT be able to read out-of-scope device, got %d", resp2.StatusCode)
	}

	_ = agent2ID // used above
}

// TestSecurity_PrivilegeEscalation_OperatorCannotCreateAdminKey verifies that
// an Operator-level API key cannot create a new API key with admin privileges.
func TestSecurity_PrivilegeEscalation_OperatorCannotCreateAdminKey(t *testing.T) {
	h := newHarness(t)

	operatorKey := h.createAPIKeyWithPerms("operator", []string{
		"devices:read", "devices:write", "jobs:read", "jobs:create",
		"api_keys:read", "api_keys:write",
	}, false)

	// Attempt to create an admin key via the API
	resp := h.apiRequestWithKey(operatorKey, "POST", "/v1/api-keys", map[string]any{
		"name":     "escalated-key",
		"role_id":  h.roleID, // Super Admin role
		"is_admin": true,
	})
	defer resp.Body.Close()

	// Should be rejected — non-admin cannot create admin keys
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Errorf("operator should not be able to create admin key, got status %d", resp.StatusCode)
	}
}

// TestSecurity_CrossTenantDeviceAccessReturns404 verifies that accessing a
// device belonging to a different tenant returns 404 (not 403) to avoid
// leaking the existence of the device.
func TestSecurity_CrossTenantDeviceAccessReturns404(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create a second tenant with its own API key
	tenant2ID := models.NewTenantID()
	_, err := h.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenant2ID, "Tenant 2", "tenant2", time.Now())
	if err != nil {
		t.Fatalf("create tenant2: %v", err)
	}

	role2ID := models.NewRoleID()
	permsJSON, _ := json.Marshal([]string{"devices:read", "jobs:read"})
	_, err = h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom) VALUES ($1, $2, $3, $4, $5)`,
		role2ID, tenant2ID, "Viewer", permsJSON, false)
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	user2ID := models.NewUserID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		user2ID, tenant2ID, "user@tenant2.local", role2ID, time.Now())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	rawKey2 := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	keyHash2 := hashKey(rawKey2)
	key2ID := models.NewAPIKeyID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		key2ID, tenant2ID, user2ID, "tenant2-key", keyHash2, role2ID, false, time.Now())
	if err != nil {
		t.Fatalf("create tenant2 key: %v", err)
	}

	// Enroll a device under tenant 1
	h.startMTLSServer()
	agentID, _, _ := h.enrollAgent("cross-tenant-host")

	// Tenant 2's key tries to access tenant 1's device — must get 404
	resp := h.apiRequestWithKey(rawKey2, "GET", "/v1/devices/"+agentID, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant device access should return 404, got %d", resp.StatusCode)
	}
}

// TestSecurity_CrossTenantJobCreationBlocked verifies that a tenant cannot
// create jobs targeting another tenant's devices.
func TestSecurity_CrossTenantJobCreationBlocked(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Enroll a device under tenant 1
	h.startMTLSServer()
	agentID, _, _ := h.enrollAgent("cross-tenant-job-host")

	// Create tenant 2 with job creation perms
	tenant2ID := models.NewTenantID()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenant2ID, "Job Tenant 2", "jobtenant2", time.Now())

	role2ID := models.NewRoleID()
	permsJSON, _ := json.Marshal([]string{"devices:read", "jobs:read", "jobs:create"})
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom) VALUES ($1, $2, $3, $4, $5)`,
		role2ID, tenant2ID, "Operator", permsJSON, false)

	user2ID := models.NewUserID()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		user2ID, tenant2ID, "op@tenant2.local", role2ID, time.Now())

	rawKey2 := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		models.NewAPIKeyID(), tenant2ID, user2ID, "t2-op-key", hashKey(rawKey2), role2ID, false, time.Now())

	// Tenant 2 tries to create a job targeting tenant 1's device
	resp := h.apiRequestWithKey(rawKey2, "POST", "/v1/jobs", map[string]any{
		"type":       "exec",
		"device_ids": []string{agentID},
		"payload":    map[string]string{"command": "id"},
	})
	defer resp.Body.Close()

	// Should fail — device doesn't exist in tenant 2
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Errorf("cross-tenant job creation should be rejected, got %d", resp.StatusCode)
	}
}

// ─── 2.9 Audit Log Integrity ─────────────────────────────

// TestSecurity_AuditLogNoUpdateOrDelete verifies that the database rules
// prevent UPDATE and DELETE on the audit_log table.
func TestSecurity_AuditLogNoUpdateOrDelete(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Insert a test audit entry
	auditID := models.NewAuditEntryID()
	_, err := h.pool.Exec(ctx,
		`INSERT INTO audit_log (id, tenant_id, actor_id, actor_type, action, resource_type, resource_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		auditID, h.tenantID, h.userID, "user", "test.action", "test", "test-resource", time.Now())
	if err != nil {
		t.Fatalf("insert audit entry: %v", err)
	}

	// Attempt UPDATE — should be silently discarded by the DO INSTEAD NOTHING rule
	tag, err := h.pool.Exec(ctx,
		`UPDATE audit_log SET action = 'tampered' WHERE id = $1`, auditID)
	if err != nil {
		t.Fatalf("UPDATE should not error (rule discards silently): %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Error("UPDATE on audit_log should affect 0 rows (blocked by rule)")
	}

	// Verify the entry is unchanged
	var action string
	err = h.pool.QueryRow(ctx, `SELECT action FROM audit_log WHERE id = $1`, auditID).Scan(&action)
	if err != nil {
		t.Fatalf("read audit entry: %v", err)
	}
	if action != "test.action" {
		t.Errorf("audit entry was modified: got %q, want %q", action, "test.action")
	}

	// Attempt DELETE — should be silently discarded
	tag, err = h.pool.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, auditID)
	if err != nil {
		t.Fatalf("DELETE should not error (rule discards silently): %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Error("DELETE on audit_log should affect 0 rows (blocked by rule)")
	}

	// Verify entry still exists
	var count int
	err = h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE id = $1`, auditID).Scan(&count)
	if err != nil {
		t.Fatalf("count audit entry: %v", err)
	}
	if count != 1 {
		t.Errorf("audit entry was deleted: count=%d, want 1", count)
	}
}

// ─── Helpers ─────────────────────────────────────────────

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func containsStr(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 &&
		fmt.Sprintf("%s", haystack) != haystack[:0] && // prevent compiler optimization
		len(haystack) >= len(needle) &&
		stringContains(haystack, needle)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
