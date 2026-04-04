//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// 20.7 — Multi-tenancy isolation

func TestMultiTenancy_DeviceIsolation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Create a second tenant with its own admin key
	tenant2ID := models.NewTenantID()
	now := time.Now().UTC()
	_, err := h.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenant2ID, "Tenant Two", "tenant-two", now)
	if err != nil {
		t.Fatalf("create tenant 2: %v", err)
	}

	role2ID := models.NewRoleID()
	permJSON, _ := json.Marshal([]string{"devices:read", "devices:write", "jobs:read", "jobs:create",
		"enrollment_tokens:write", "groups:read", "groups:write", "tags:read", "tags:write"})
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom)
		 VALUES ($1, $2, $3, $4, $5)`,
		role2ID, tenant2ID, "Admin", permJSON, false)

	user2ID := models.NewUserID()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		user2ID, tenant2ID, "admin@tenant2.local", role2ID, now)

	rawKey2 := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	keyHash2 := hashKey(rawKey2)
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		models.NewAPIKeyID(), tenant2ID, user2ID, "admin-key-2", keyHash2, role2ID, true, now)

	// Enroll a device under tenant 1 (uses h.enrollAgent which creates token under h.tenantID)
	agentID1, _, _ := h.enrollAgent("tenant1-host")

	// Enroll a device under tenant 2 (manually create token + device)
	device2ID := models.NewDeviceID()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO devices (id, tenant_id, hostname, os, os_version, arch, agent_version, status, registered_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		device2ID, tenant2ID, "tenant2-host", "linux", "Ubuntu 24.04", "amd64", "1.0.0", "online", now)

	// Tenant 1 lists devices — should only see their device
	resp := h.apiRequestWithKey(h.adminKey, "GET", "/v1/devices", nil)
	assertStatus(t, resp, http.StatusOK)

	var listResp1 struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	readJSON(t, resp, &listResp1)

	for _, d := range listResp1.Data {
		if d.ID == device2ID {
			t.Error("tenant 1 can see tenant 2's device — isolation breach!")
		}
	}

	// Verify tenant 1's device is visible
	found := false
	for _, d := range listResp1.Data {
		if d.ID == agentID1 {
			found = true
		}
	}
	if !found {
		t.Error("tenant 1 cannot see its own device")
	}

	// Tenant 2 lists devices — should only see their device
	resp = h.apiRequestWithKey(rawKey2, "GET", "/v1/devices", nil)
	assertStatus(t, resp, http.StatusOK)

	var listResp2 struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	readJSON(t, resp, &listResp2)

	for _, d := range listResp2.Data {
		if d.ID == agentID1 {
			t.Error("tenant 2 can see tenant 1's device — isolation breach!")
		}
	}
}

func TestMultiTenancy_JobIsolation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Create second tenant + API key
	tenant2ID := models.NewTenantID()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenant2ID, "Tenant Two", "tenant-two", now)

	role2ID := models.NewRoleID()
	permJSON, _ := json.Marshal([]string{"jobs:read", "jobs:create", "devices:read"})
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom)
		 VALUES ($1, $2, $3, $4, $5)`,
		role2ID, tenant2ID, "Admin", permJSON, false)
	user2ID := models.NewUserID()
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		user2ID, tenant2ID, "admin@t2.local", role2ID, now)

	rawKey2 := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	h2 := sha256.Sum256([]byte(rawKey2))
	_, _ = h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		models.NewAPIKeyID(), tenant2ID, user2ID, "key2", hex.EncodeToString(h2[:]), role2ID, true, now)

	// Create a job under tenant 1
	agentID, _, _ := h.enrollAgent("job-iso-host")
	payload, _ := json.Marshal(map[string]string{"command": "echo t1"})
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	assertStatus(t, resp, http.StatusCreated)
	var createResp struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createResp)
	jobID := createResp.JobIDs[0]

	// Tenant 2 tries to view the job — should not find it
	resp = h.apiRequestWithKey(rawKey2, "GET", "/v1/jobs/"+jobID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tenant 2 accessing tenant 1's job: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tenant 2 lists jobs — should be empty
	resp = h.apiRequestWithKey(rawKey2, "GET", "/v1/jobs", nil)
	assertStatus(t, resp, http.StatusOK)

	var jobList struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	readJSON(t, resp, &jobList)

	for _, j := range jobList.Data {
		if j.ID == jobID {
			t.Error("tenant 2 can see tenant 1's job — isolation breach!")
		}
	}
}
