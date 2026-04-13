//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// setTenantQuotas overwrites the test tenant's TenantConfig.Quotas
// JSONB payload in-place. The quota.Resolver reads tenant config on
// every check, so the next request picks up the new limits without a
// server restart.
func (h *testHarness) setTenantQuotas(q *models.TenantQuotas) {
	h.t.Helper()
	cfg := &models.TenantConfig{Quotas: q}
	b, err := json.Marshal(cfg)
	if err != nil {
		h.t.Fatalf("marshal tenant config: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE tenants SET config = $1 WHERE id = $2`, b, h.tenantID); err != nil {
		h.t.Fatalf("update tenant config: %v", err)
	}
}

// readErrorCode extracts the `error.code` field from a standard API
// error envelope so tests can assert on the machine-readable code.
func readErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, string(body))
	}
	return env.Error.Code
}

// ─── 2.8 Per-Tenant Resource Quotas ───────────────────────

// TestQuota_APIKeys_Exceeded verifies that once the per-tenant API
// key ceiling is reached, POST /v1/api-keys returns 409
// quota_exceeded. The bootstrap harness already creates one admin
// key, so setting MaxAPIKeys=1 puts the tenant exactly at the cap.
func TestQuota_APIKeys_Exceeded(t *testing.T) {
	h := newHarness(t)
	h.setTenantQuotas(&models.TenantQuotas{MaxAPIKeys: 1})

	resp := h.apiRequest("POST", "/v1/api-keys", map[string]any{
		"name": "over-cap",
	})
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, string(body))
	}
	if code := readErrorCode(t, resp); code != "quota_exceeded" {
		t.Errorf("expected error code quota_exceeded, got %q", code)
	}
}

// TestQuota_APIKeys_UnderCap_Allowed verifies the opposite: with
// headroom under the ceiling, key creation still succeeds. Guards
// against an overzealous check that would reject everything.
func TestQuota_APIKeys_UnderCap_Allowed(t *testing.T) {
	h := newHarness(t)
	h.setTenantQuotas(&models.TenantQuotas{MaxAPIKeys: 10})

	resp := h.apiRequest("POST", "/v1/api-keys", map[string]any{
		"name":    "under-cap",
		"role_id": h.roleID,
	})
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusCreated)
}

// TestQuota_Devices_Exceeded verifies that enrollment is rejected
// once the device ceiling is reached. The first enrollment succeeds,
// the second is rejected with quota_exceeded.
func TestQuota_Devices_Exceeded(t *testing.T) {
	h := newHarness(t)
	h.setTenantQuotas(&models.TenantQuotas{MaxDevices: 1})

	// First enrollment should succeed
	token1 := h.createEnrollmentToken()
	if status := h.tryEnrollAgent("host-1", token1); status != http.StatusOK {
		t.Fatalf("first enroll expected 200, got %d", status)
	}

	// Second enrollment should fail with 409 — a fresh token still
	// gets consumed, but the quota check rejects before the device
	// is inserted.
	token2 := h.createEnrollmentToken()
	if status := h.tryEnrollAgent("host-2", token2); status != http.StatusConflict {
		t.Fatalf("second enroll expected 409, got %d", status)
	}
}

// TestQuota_QueuedJobs_ExceededOnFanOut verifies that a POST /v1/jobs
// whose target fan-out would push the tenant over the queued-job cap
// is rejected atomically — no partial batch lands.
func TestQuota_QueuedJobs_ExceededOnFanOut(t *testing.T) {
	h := newHarness(t)
	// Enroll two devices so fan-out is 2
	agent1ID, _, _ := h.enrollAgent("job-host-1")
	agent2ID, _, _ := h.enrollAgent("job-host-2")

	// Cap = 1, fan-out = 2 → reject
	h.setTenantQuotas(&models.TenantQuotas{MaxQueuedJobs: 1})

	payload, _ := json.Marshal(map[string]any{"command": "echo hi"})
	resp := h.apiRequest("POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agent1ID, agent2ID}},
		"payload": json.RawMessage(payload),
	})
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, string(body))
	}
	if code := readErrorCode(t, resp); code != "quota_exceeded" {
		t.Errorf("expected error code quota_exceeded, got %q", code)
	}

	// Atomicity: confirm no jobs landed for this tenant
	var count int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM jobs WHERE tenant_id = $1`, h.tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 0 {
		t.Errorf("partial batch landed: %d jobs created despite quota rejection", count)
	}
}

// TestQuota_QueuedJobs_CountsOnlyActive verifies that jobs in
// terminal states do not count against the queued-job ceiling.
// Otherwise long-lived tenants would eventually hit a permanent cap
// from historical work.
func TestQuota_QueuedJobs_CountsOnlyActive(t *testing.T) {
	h := newHarness(t)
	agentID, _, _ := h.enrollAgent("history-host")
	ctx := context.Background()

	// Plant 10 completed jobs — should not count against the cap
	for i := 0; i < 10; i++ {
		_, err := h.pool.Exec(ctx,
			`INSERT INTO jobs (id, tenant_id, device_id, type, status, payload, created_at)
			 VALUES ($1, $2, $3, 'exec', 'completed', '{}', now())`,
			models.NewJobID(), h.tenantID, agentID)
		if err != nil {
			t.Fatalf("insert historical job: %v", err)
		}
	}

	// Cap of 5 active jobs — creating 1 new job must succeed because
	// the 10 completed ones do not count.
	h.setTenantQuotas(&models.TenantQuotas{MaxQueuedJobs: 5})

	payload, _ := json.Marshal(map[string]any{"command": "echo fresh"})
	resp := h.apiRequest("POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusCreated)
}

// TestQuota_FileSize_Exceeded verifies that an upload initiated with
// size_bytes above the ceiling is rejected before any metadata row
// is written.
func TestQuota_FileSize_Exceeded(t *testing.T) {
	h := newHarness(t)
	h.setTenantQuotas(&models.TenantQuotas{MaxFileSizeBytes: 1024}) // 1 KB

	resp := h.apiRequest("POST", "/v1/files", map[string]any{
		"filename":   "large.bin",
		"size_bytes": 1 << 20, // 1 MB — above the cap
		"sha256":     "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 409, got %d: %s", resp.StatusCode, string(body))
	}
	if code := readErrorCode(t, resp); code != "quota_exceeded" {
		t.Errorf("expected error code quota_exceeded, got %q", code)
	}

	// No metadata row should have been written
	var count int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE tenant_id = $1`, h.tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if count != 0 {
		t.Errorf("file row leaked on quota rejection: %d rows", count)
	}
}

// TestQuota_FileSize_UnderCap_Allowed verifies that a file under the
// ceiling still initiates normally.
func TestQuota_FileSize_UnderCap_Allowed(t *testing.T) {
	h := newHarness(t)
	h.setTenantQuotas(&models.TenantQuotas{MaxFileSizeBytes: 10 * 1024 * 1024})

	resp := h.apiRequest("POST", "/v1/files", map[string]any{
		"filename":   "small.bin",
		"size_bytes": 1024,
		"sha256":     "1111111111111111111111111111111111111111111111111111111111111111",
	})
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusCreated)
}
