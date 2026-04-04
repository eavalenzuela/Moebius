//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// 20.5 — Agent update end-to-end

func TestAgentUpdate_JobDispatched(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("update-host")

	// Create an agent_update job
	payload, _ := json.Marshal(protocol.AgentUpdatePayload{
		Version:     "2.0.0",
		Channel:     "stable",
		DownloadURL: "https://example.com/agent-2.0.0.tar.gz",
		SHA256:      "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Signature:   "c2lnbmF0dXJl",
		SizeBytes:   10_000_000,
	})
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "agent_update",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	assertStatus(t, resp, http.StatusCreated)

	var createResp struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createResp)
	jobID := createResp.JobIDs[0]

	// Agent checks in and receives the update job
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	if len(checkinResp.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(checkinResp.Jobs))
	}
	if checkinResp.Jobs[0].Type != "agent_update" {
		t.Errorf("job type = %q, want %q", checkinResp.Jobs[0].Type, "agent_update")
	}

	// Parse the payload
	var updatePayload protocol.AgentUpdatePayload
	if err := json.Unmarshal(checkinResp.Jobs[0].Payload, &updatePayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if updatePayload.Version != "2.0.0" {
		t.Errorf("version = %q, want %q", updatePayload.Version, "2.0.0")
	}
	if updatePayload.SHA256 == "" {
		t.Error("sha256 is empty")
	}

	// Verify job is DISPATCHED
	var status string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusDispatched {
		t.Errorf("status = %q, want %q", status, models.JobStatusDispatched)
	}
}

func TestAgentUpdate_RollbackReported(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("rollback-host")

	// Create update job and dispatch it
	payload, _ := json.Marshal(protocol.AgentUpdatePayload{
		Version:     "2.0.0",
		DownloadURL: "https://example.com/agent-2.0.0.tar.gz",
		SHA256:      "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
	})
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "agent_update",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	assertStatus(t, resp, http.StatusCreated)
	var createResp struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createResp)
	jobID := createResp.JobIDs[0]

	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// First checkin: receive update job
	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Mark job as acknowledged in DB (simulating agent ack)
	_, _ = h.pool.Exec(context.Background(),
		`UPDATE jobs SET status = 'acknowledged', acknowledged_at = $1 WHERE id = $2`,
		time.Now().UTC(), jobID)

	// Second checkin: agent reports rollback failure
	checkin.Sequence = 2
	checkin.Status.LastUpdateFailed = true
	checkin.Status.LastUpdateJobID = jobID
	checkin.Status.LastUpdateError = "version mismatch after restart"

	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Job should now be failed
	var status string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusFailed {
		t.Errorf("status = %q, want %q", status, models.JobStatusFailed)
	}

	// Verify last_error is set
	var lastError string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT COALESCE(last_error, '') FROM jobs WHERE id = $1`, jobID).Scan(&lastError)
	if lastError != "version mismatch after restart" {
		t.Errorf("last_error = %q, want 'version mismatch after restart'", lastError)
	}
}
