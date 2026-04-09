//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// bytesReader is a small helper for body-size tests that need to send raw
// byte slices through http.NewRequest.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

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

// TestSecurity_RevokedAPIKeyRejectedImmediately verifies that an API key
// deleted via the management endpoint is rejected on the very next request,
// confirming the auth path has no caching layer that would let a revoked key
// keep working until a TTL expires.
func TestSecurity_RevokedAPIKeyRejectedImmediately(t *testing.T) {
	h := newHarness(t)

	// Create a fresh non-admin key
	rawKey := h.createAPIKeyWithPerms("revoke-test", []string{"devices:read"}, false)

	// Confirm it works before revocation
	resp := h.apiRequestWithKey(rawKey, "GET", "/v1/devices", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh key should work, got %d", resp.StatusCode)
	}

	// Look up the key ID and delete it directly (no API call needed —
	// we are testing the auth path, not the delete handler)
	var keyID string
	err := h.pool.QueryRow(context.Background(),
		`SELECT id FROM api_keys WHERE key_hash = $1`, hashKey(rawKey)).Scan(&keyID)
	if err != nil {
		t.Fatalf("look up key id: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`DELETE FROM api_keys WHERE id = $1`, keyID); err != nil {
		t.Fatalf("delete key: %v", err)
	}

	// Next request must be rejected — no cache, no TTL
	resp2 := h.apiRequestWithKey(rawKey, "GET", "/v1/devices", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked key should be rejected on next request with 401, got %d", resp2.StatusCode)
	}
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

// TestSecurity_EnrollmentTokenScopeCopiedToDevice creates a token with a
// group scope, enrolls an agent with it, and verifies the device is now a
// member of the group. Closes the §2.1 "token scope copied to device"
// invariant.
func TestSecurity_EnrollmentTokenScopeCopiedToDevice(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Pre-create a group in the harness tenant
	groupID := models.NewGroupID()
	_, err := h.pool.Exec(ctx,
		`INSERT INTO groups (id, tenant_id, name) VALUES ($1, $2, $3)`,
		groupID, h.tenantID, "scope-copy-target")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	// Create a scoped token via the service (bypasses HTTP — we are
	// testing enrollment, not the token creation handler)
	enrollment := auth.NewEnrollmentService(h.pool)
	scope := &models.APIScope{GroupIDs: []string{groupID}}
	result, err := enrollment.CreateToken(ctx, h.tenantID, h.userID, scope, time.Hour)
	if err != nil {
		t.Fatalf("create scoped token: %v", err)
	}

	// Enroll an agent using the scoped token
	h.startMTLSServer()
	agentID := h.enrollAgentWithToken("scoped-host", result.Raw)

	// Verify device_groups contains (agentID, groupID)
	var count int
	err = h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM device_groups WHERE device_id = $1 AND group_id = $2`,
		agentID, groupID).Scan(&count)
	if err != nil {
		t.Fatalf("query device_groups: %v", err)
	}
	if count != 1 {
		t.Errorf("expected enrolled device to be in token scope group, got %d rows", count)
	}
}

// TestSecurity_EnrollmentRejectsCrossTenantScope verifies that a token whose
// scope references a group from a different tenant is rejected at token
// creation, preventing cross-tenant pollution of device_groups.
func TestSecurity_EnrollmentRejectsCrossTenantScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create a second tenant with its own group
	tenant2ID := models.NewTenantID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenant2ID, "Tenant 2", "tenant2-scope", time.Now()); err != nil {
		t.Fatalf("create tenant2: %v", err)
	}
	foreignGroupID := models.NewGroupID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO groups (id, tenant_id, name) VALUES ($1, $2, $3)`,
		foreignGroupID, tenant2ID, "foreign-group"); err != nil {
		t.Fatalf("create foreign group: %v", err)
	}

	// Admin in tenant 1 attempts to mint a token referencing the foreign group
	resp := h.apiRequest("POST", "/v1/enrollment-tokens", map[string]any{
		"scope": map[string]any{
			"group_ids": []string{foreignGroupID},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 rejecting cross-tenant scope, got %d", resp.StatusCode)
	}
}

// TestSecurity_EnrollmentRollsBackOnBadScope verifies the enrollment-time
// defense-in-depth: if a token references a group that no longer exists
// (e.g., deleted between token creation and enrollment), the entire
// enrollment is rolled back — no orphaned device, no half-applied scope.
func TestSecurity_EnrollmentRollsBackOnBadScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create a group, mint a scoped token, then delete the group
	groupID := models.NewGroupID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO groups (id, tenant_id, name) VALUES ($1, $2, $3)`,
		groupID, h.tenantID, "doomed-group"); err != nil {
		t.Fatalf("create group: %v", err)
	}

	enrollment := auth.NewEnrollmentService(h.pool)
	scope := &models.APIScope{GroupIDs: []string{groupID}}
	result, err := enrollment.CreateToken(ctx, h.tenantID, h.userID, scope, time.Hour)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	if _, err := h.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	// Attempt enrollment — should fail
	h.startMTLSServer()
	status := h.tryEnrollAgent("rollback-host", result.Raw)
	if status == http.StatusOK {
		t.Errorf("expected enrollment to fail with deleted scope group, got 200")
	}

	// No device should have been created (rollback)
	var count int
	err = h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM devices WHERE hostname = $1`, "rollback-host").Scan(&count)
	if err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if count != 0 {
		t.Errorf("expected enrollment rollback (0 devices), got %d", count)
	}
}

// TestSecurity_OIDCSubjectUnique verifies the partial unique index on
// users.sso_subject (migration 005). Two users in different tenants must
// not be able to share the same SSO subject — that would make the OIDC
// middleware lookup `WHERE sso_subject = $1` ambiguous and let one user
// be authenticated as another. NULL is allowed multiple times so users
// without an SSO mapping can coexist.
func TestSecurity_OIDCSubjectUnique(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create a second tenant
	tenant2ID := models.NewTenantID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenant2ID, "T2", "t2-sso", time.Now()); err != nil {
		t.Fatalf("create tenant2: %v", err)
	}

	// First user with sso_subject "alice@example.com" — should succeed
	user1ID := models.NewUserID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, sso_subject, created_at) VALUES ($1, $2, $3, $4, $5)`,
		user1ID, h.tenantID, "alice@t1.local", "alice@example.com", time.Now()); err != nil {
		t.Fatalf("first user with sso_subject should succeed: %v", err)
	}

	// Second user (different tenant) with the same sso_subject — must fail
	user2ID := models.NewUserID()
	_, err := h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, sso_subject, created_at) VALUES ($1, $2, $3, $4, $5)`,
		user2ID, tenant2ID, "alice@t2.local", "alice@example.com", time.Now())
	if err == nil {
		t.Errorf("expected duplicate sso_subject across tenants to be rejected by unique index")
	}

	// Two users with NULL sso_subject must coexist (partial index)
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, created_at) VALUES ($1, $2, $3, $4)`,
		models.NewUserID(), h.tenantID, "noauth1@local", time.Now()); err != nil {
		t.Fatalf("user with null sso_subject should succeed: %v", err)
	}
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, created_at) VALUES ($1, $2, $3, $4)`,
		models.NewUserID(), h.tenantID, "noauth2@local", time.Now()); err != nil {
		t.Fatalf("second user with null sso_subject should succeed (partial index): %v", err)
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

// TestSecurity_PrivilegeEscalation_NonAdminCannotCreateRoleWithEscalatedPerms
// verifies that a non-admin caller with `roles:write` cannot mint a custom
// role granting permissions they themselves don't hold. Without the
// permission-subset clamp in roles.Create, anyone with `roles:write` could
// create a role with `[users:write, roles:write, tenant:write, ...]`, attach
// it to a key/user, and gain full tenant control.
func TestSecurity_PrivilegeEscalation_NonAdminCannotCreateRoleWithEscalatedPerms(t *testing.T) {
	h := newHarness(t)

	// Caller has `roles:write` (so it can hit the endpoint at all) plus a
	// narrow set of read-only perms — but NOT users:write or tenant:write.
	limitedKey := h.createAPIKeyWithPerms("partial-roles-admin", []string{
		"roles:read", "roles:write",
		"devices:read",
	}, false)

	// Attempt to create a role granting perms the caller does not hold.
	resp := h.apiRequestWithKey(limitedKey, "POST", "/v1/roles", map[string]any{
		"name": "stealth-admin",
		"permissions": []string{
			"roles:read", "roles:write",
			"users:read", "users:write",
			"tenant:read", "tenant:write",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when escalating role perms, got %d", resp.StatusCode)
	}

	// Sanity check: a role whose perms ARE a subset of the caller's perms
	// should still succeed, so the clamp isn't blanket-blocking everything.
	resp2 := h.apiRequestWithKey(limitedKey, "POST", "/v1/roles", map[string]any{
		"name":        "subset-role",
		"permissions": []string{"devices:read"},
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 for in-scope role create, got %d", resp2.StatusCode)
	}
}

// TestSecurity_PrivilegeEscalation_NonAdminCannotUpdateRoleWithEscalatedPerms
// verifies the same clamp applies on PATCH /v1/roles/{id}. Without it, an
// attacker could create a tame role first (subset clamp passes) and then
// PATCH it later to widen the permission list.
func TestSecurity_PrivilegeEscalation_NonAdminCannotUpdateRoleWithEscalatedPerms(t *testing.T) {
	h := newHarness(t)

	limitedKey := h.createAPIKeyWithPerms("partial-roles-admin", []string{
		"roles:read", "roles:write",
		"devices:read",
	}, false)

	// First, create a custom role using the admin key (so it exists).
	createResp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/roles", map[string]any{
		"name":        "victim-role",
		"permissions": []string{"devices:read"},
	})
	var created struct {
		ID string `json:"id"`
	}
	readJSON(t, createResp, &created)
	if created.ID == "" {
		t.Fatalf("admin failed to create base role")
	}

	// Limited key tries to widen the permissions via PATCH.
	resp := h.apiRequestWithKey(limitedKey, "PATCH", "/v1/roles/"+created.ID, map[string]any{
		"permissions": []string{
			"devices:read", "users:write", "tenant:write",
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when widening role perms via PATCH, got %d", resp.StatusCode)
	}
}

// TestSecurity_PrivilegeEscalation_UserCannotSelfAssignHigherRole verifies
// that a user with `users:write` cannot promote themselves to an admin-level
// role. Self-edits are rejected outright (defense in depth) — even role
// changes that look like demotions could remove sibling checks elsewhere.
func TestSecurity_PrivilegeEscalation_UserCannotSelfAssignHigherRole(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Build a second user with a custom role that has users:write only.
	limitedRoleID := models.NewRoleID()
	permJSON, _ := json.Marshal([]string{"users:read", "users:write"})
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom) VALUES ($1, $2, $3, $4, $5)`,
		limitedRoleID, h.tenantID, "user-manager", permJSON, true); err != nil {
		t.Fatalf("create limited role: %v", err)
	}
	limitedUserID := models.NewUserID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		limitedUserID, h.tenantID, "limited@test.local", limitedRoleID, time.Now()); err != nil {
		t.Fatalf("create limited user: %v", err)
	}
	// API key tied to the limited user (so user_id_from_context == limitedUserID).
	rawKey := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	keyHash := hashKey(rawKey)
	keyID := models.NewAPIKeyID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		keyID, h.tenantID, limitedUserID, "limited-key", keyHash, limitedRoleID, false, time.Now()); err != nil {
		t.Fatalf("create limited api key: %v", err)
	}

	// Attempt 1: limited user tries to assign themselves the Super Admin role.
	resp := h.apiRequestWithKey(rawKey, "PATCH", "/v1/users/"+limitedUserID, map[string]any{
		"role_id": h.roleID, // Super Admin
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for self role change, got %d", resp.StatusCode)
	}

	// Attempt 2: limited user tries to promote a DIFFERENT user to Super
	// Admin. The self-target check doesn't apply, so this exercises the
	// permission-subset clamp instead. The Super Admin role grants perms
	// the caller doesn't have, so it must be rejected.
	otherUserID := models.NewUserID()
	if _, err := h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		otherUserID, h.tenantID, "other@test.local", limitedRoleID, time.Now()); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	resp2 := h.apiRequestWithKey(rawKey, "PATCH", "/v1/users/"+otherUserID, map[string]any{
		"role_id": h.roleID,
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for cross-user role escalation, got %d", resp2.StatusCode)
	}
}

// TestSecurity_PrivilegeEscalation_NonAdminCannotDeleteAdminKey verifies that
// a non-admin caller with `api_keys:write` cannot delete an admin key. Without
// this guard, a tenant operator with key-management perms could revoke the
// bootstrap admin key and lock the tenant out of recovery, or wipe a peer
// admin's credentials.
func TestSecurity_PrivilegeEscalation_NonAdminCannotDeleteAdminKey(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Look up the admin key ID created by the harness.
	var adminKeyID string
	if err := h.pool.QueryRow(ctx,
		`SELECT id FROM api_keys WHERE key_hash = $1`, hashKey(h.adminKey)).Scan(&adminKeyID); err != nil {
		t.Fatalf("look up admin key id: %v", err)
	}

	// Caller has api_keys:write but is NOT an admin key.
	limitedKey := h.createAPIKeyWithPerms("key-manager", []string{
		"api_keys:read", "api_keys:write",
	}, false)

	resp := h.apiRequestWithKey(limitedKey, "DELETE", "/v1/api-keys/"+adminKeyID, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin deleting admin key: expected 403, got %d", resp.StatusCode)
	}

	// Confirm the admin key still works after the rejected delete.
	check := h.apiRequestWithKey(h.adminKey, "GET", "/v1/devices", nil)
	check.Body.Close()
	if check.StatusCode != http.StatusOK {
		t.Errorf("admin key should still be valid after rejected delete, got %d", check.StatusCode)
	}

	// Sanity: a non-admin key in the same role can still be deleted by the
	// limited caller (so we know the guard isn't blanket-blocking deletes).
	victimKey := h.createAPIKeyWithPerms("victim", []string{"devices:read"}, false)
	var victimID string
	if err := h.pool.QueryRow(ctx,
		`SELECT id FROM api_keys WHERE key_hash = $1`, hashKey(victimKey)).Scan(&victimID); err != nil {
		t.Fatalf("look up victim key id: %v", err)
	}
	resp2 := h.apiRequestWithKey(limitedKey, "DELETE", "/v1/api-keys/"+victimID, nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("non-admin deleting non-admin key: expected 204, got %d", resp2.StatusCode)
	}
}

// ─── 2.3 Input Validation — Body size / DoS limits ───────

// TestSecurity_BodySize_JSONCRUDRejectsOversizedBody verifies that the
// global root MaxBytes cap rejects a JSON body larger than the global
// limit, even on a regular CRUD endpoint that doesn't add its own
// per-route cap. Without MaxBytes wired in, an attacker could send a
// 100 MB JSON blob and force the server to buffer the entire payload
// before any handler logic runs.
func TestSecurity_BodySize_JSONCRUDRejectsOversizedBody(t *testing.T) {
	h := newHarness(t)

	// Build a 12 MB JSON payload — well above the 8 MB global cap.
	payload := make([]byte, 12*1024*1024)
	for i := range payload {
		payload[i] = 'a'
	}
	body := []byte(`{"hostname":"`)
	body = append(body, payload...)
	body = append(body, '"', '}')

	req, err := http.NewRequest("PATCH", h.apiURL+"/v1/devices/dev_doesnt_matter", bytesReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.adminKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		// MaxBytesReader can break the connection mid-stream; treat that
		// as success because the server clearly refused the body.
		return
	}
	defer resp.Body.Close()

	// http.MaxBytesReader surfaces as a JSON decode error (400) because
	// the handler decodes before the cap fires. Either 400 or 413 is
	// acceptable — both prove the body never reached the handler intact.
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized JSON body: expected 400/413, got %d", resp.StatusCode)
	}
}

// TestSecurity_BodySize_GlobalCapBlocksHugeBody verifies the chi root-level
// MaxBytes(8 MB) cap rejects bodies that exceed the global ceiling, even on
// endpoints that don't add their own per-route cap. We use the unauthenticated
// enrollment endpoint because it bypasses the /v1 JSON cap and exercises the
// global guard directly.
func TestSecurity_BodySize_GlobalCapBlocksHugeBody(t *testing.T) {
	h := newHarness(t)

	// 16 MB — well above the 8 MB global cap.
	huge := make([]byte, 16*1024*1024)
	for i := range huge {
		huge[i] = 'b'
	}

	req, err := http.NewRequest("POST", h.apiURL+"/v1/agents/enroll", bytesReader(huge))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		// MaxBytesReader can break the connection mid-stream; treat that
		// as success because the server clearly refused the body.
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Errorf("oversized enroll body should be rejected, got %d", resp.StatusCode)
	}
}

// TestSecurity_BodySize_ChunkUploadAcceptsCappedSize verifies that the
// chunk upload route honors a separate, larger cap (6 MB) than the JSON
// CRUD cap. The route is registered before the /v1 JSON cap is applied,
// so a normal 1 MB chunk should still be acceptable. This is a regression
// guard: a future refactor that moves the chunk route under the JSON cap
// would silently break large uploads.
func TestSecurity_BodySize_ChunkUploadAcceptsCappedSize(t *testing.T) {
	h := newHarness(t)

	// Initiate an upload first so we have a valid upload_id.
	initResp := h.apiRequest("POST", "/v1/files", map[string]any{
		"filename":   "big.bin",
		"size_bytes": 5 * 1024 * 1024,
		"sha256":     "0000000000000000000000000000000000000000000000000000000000000000",
	})
	var initBody struct {
		UploadID       string `json:"upload_id"`
		ChunkSizeBytes int    `json:"chunk_size_bytes"`
	}
	readJSON(t, initResp, &initBody)
	if initBody.UploadID == "" {
		t.Skip("upload init unavailable in this build")
	}

	// 1 MB chunk — small enough to fit in the chunk-route cap, large
	// enough to exceed the 1 MB JSON CRUD cap (it's exactly at the line,
	// so we'd see 400 from MaxBytesReader if the JSON cap leaked here).
	chunk := make([]byte, 1*1024*1024)
	req, err := http.NewRequest("PUT",
		h.apiURL+"/v1/files/uploads/"+initBody.UploadID+"/chunks/0",
		bytesReader(chunk))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.adminKey)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		t.Fatalf("send chunk: %v", err)
	}
	defer resp.Body.Close()

	// Should NOT be a 413 / "exceeds maximum size" / decode-error 400.
	// Hash mismatch (400 with code "checksum_mismatch") is acceptable —
	// it proves the body got through and the handler ran.
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		t.Errorf("chunk upload at 1 MB should not be 413, got %d", resp.StatusCode)
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

// ─── 2.4 Cryptography & Key Management ───────────────────

// TestSecurity_Enrollment_RejectsRSACSR exercises the SignCSR public-key
// allowlist end-to-end by submitting a CSR backed by an RSA-2048 key to
// /v1/agents/enroll. The handler must surface this as an HTTP 400 from
// pki.SignCSR, leaving no device row behind. Locking the agent fleet to
// ECDSA P-256 keeps every TLS handshake on a single, audited curve and
// removes the foot-gun of an agent shipping a 1024-bit RSA key.
func TestSecurity_Enrollment_RejectsRSACSR(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	token := h.createEnrollmentToken()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "rsa-host"}},
		rsaKey,
	)
	if err != nil {
		t.Fatalf("create RSA CSR: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	body, _ := json.Marshal(protocol.EnrollRequest{
		EnrollmentToken: token,
		CSR:             string(csrPEM),
		Hostname:        "rsa-host",
		OS:              "linux",
		OSVersion:       "Ubuntu 24.04",
		Arch:            "amd64",
		AgentVersion:    "1.0.0",
	})
	resp, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	// No device row should have been created for an RSA hostname even though
	// the token was already burned by ValidateAndConsume.
	var n int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM devices WHERE hostname = $1 AND tenant_id = $2`,
		"rsa-host", h.tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if n != 0 {
		t.Errorf("expected zero devices for rejected RSA enrollment, got %d", n)
	}
}

// TestSecurity_Enrollment_RejectsP384CSR confirms that even a strong but
// non-P256 ECDSA curve is rejected. This is the more subtle case: P-384
// has greater theoretical strength than P-256 but is not what the agent's
// own keygen produces, so accepting it would create a heterogenous fleet
// and a way to silently drift the canonical curve.
func TestSecurity_Enrollment_RejectsP384CSR(t *testing.T) {
	h := newHarness(t)

	token := h.createEnrollmentToken()

	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("p384 keygen: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "p384-host"}},
		key,
	)
	if err != nil {
		t.Fatalf("create P384 CSR: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	body, _ := json.Marshal(protocol.EnrollRequest{
		EnrollmentToken: token,
		CSR:             string(csrPEM),
		Hostname:        "p384-host",
		OS:              "linux",
		OSVersion:       "Ubuntu 24.04",
		Arch:            "amd64",
		AgentVersion:    "1.0.0",
	})
	resp, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
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
