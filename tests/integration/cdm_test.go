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

// 20.3 — CDM end-to-end

func TestCDM_JobsHeldWithoutSession(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("cdm-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Create a job
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo cdm-test"})
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

	// Agent checks in with CDM enabled but no session
	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status: protocol.AgentStatus{
			AgentVersion:     "1.0.0",
			CDMEnabled:       true,
			CDMSessionActive: false,
		},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	// No jobs should be dispatched
	if len(checkinResp.Jobs) != 0 {
		t.Errorf("expected 0 jobs (CDM hold), got %d", len(checkinResp.Jobs))
	}

	// Job should be in CDM_HOLD
	var status string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusCDMHold {
		t.Errorf("job status = %q, want %q", status, models.JobStatusCDMHold)
	}
}

func TestCDM_SessionGrantReleasesJobs(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("cdm-release-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Create a job
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo released"})
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

	// First checkin: CDM enabled, no session → job held
	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status: protocol.AgentStatus{
			AgentVersion:     "1.0.0",
			CDMEnabled:       true,
			CDMSessionActive: false,
		},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Verify CDM_HOLD
	var status string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID).Scan(&status)
	if status != models.JobStatusCDMHold {
		t.Fatalf("expected CDM_HOLD, got %q", status)
	}

	// Second checkin: CDM enabled, session active → job released and dispatched
	expires := time.Now().Add(1 * time.Hour)
	checkin.Sequence = 2
	checkin.Status.CDMSessionActive = true
	checkin.Status.CDMSessionExpiresAt = &expires

	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	if len(checkinResp.Jobs) != 1 {
		t.Fatalf("expected 1 released job, got %d", len(checkinResp.Jobs))
	}
	if checkinResp.Jobs[0].JobID != jobID {
		t.Errorf("job ID = %q, want %q", checkinResp.Jobs[0].JobID, jobID)
	}
}

func TestCDM_RevokeSessionHoldsNewJobs(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("cdm-revoke-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Create first job and dispatch it with session active
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo first"})
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Checkin with session active
	expires := time.Now().Add(1 * time.Hour)
	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status: protocol.AgentStatus{
			AgentVersion:        "1.0.0",
			CDMEnabled:          true,
			CDMSessionActive:    true,
			CDMSessionExpiresAt: &expires,
		},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Create second job
	payload2, _ := json.Marshal(protocol.ExecPayload{Command: "echo second"})
	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload2),
	})
	assertStatus(t, resp, http.StatusCreated)
	var createResp struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createResp)
	secondJobID := createResp.JobIDs[0]

	// Checkin with session revoked
	checkin.Sequence = 2
	checkin.Status.CDMSessionActive = false
	checkin.Status.CDMSessionExpiresAt = nil

	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	// No jobs should be dispatched
	if len(checkinResp.Jobs) != 0 {
		t.Errorf("expected 0 jobs after session revoke, got %d", len(checkinResp.Jobs))
	}

	// Second job should be CDM_HOLD
	var status string
	_ = h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, secondJobID).Scan(&status)
	if status != models.JobStatusCDMHold {
		t.Errorf("second job status = %q, want %q", status, models.JobStatusCDMHold)
	}
}
