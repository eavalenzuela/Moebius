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

// TestCDM_SessionExpiryAllowsCompletionBlocksNewWork covers SEC_VALIDATION
// §2.6 — "CDM session expiry: in-flight job completes, no new jobs start".
//
// Scenario:
//  1. Session active → job A is dispatched, agent acknowledges it.
//  2. Server creates job B (still queued).
//  3. Session expires on the agent. Agent reports CDMSessionActive=false on
//     subsequent requests.
//  4. Agent submits the completion result for the in-flight job A. The result
//     endpoint MUST accept it — there is no CDM gate on result submission, so
//     work that was already running can finish reporting back.
//  5. Agent re-checks in. Server MUST return zero new jobs and move job B to
//     CDM_HOLD, mirroring the cold-start hold behaviour.
func TestCDM_SessionExpiryAllowsCompletionBlocksNewWork(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("cdm-expiry-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Create job A.
	payloadA, _ := json.Marshal(protocol.ExecPayload{Command: "echo A"})
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payloadA),
	})
	assertStatus(t, resp, http.StatusCreated)
	var createA struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createA)
	jobA := createA.JobIDs[0]

	// Checkin with session active → job A dispatched.
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
	var firstResp protocol.CheckinResponse
	readJSON(t, resp, &firstResp)
	if len(firstResp.Jobs) != 1 || firstResp.Jobs[0].JobID != jobA {
		t.Fatalf("expected job A dispatched while session active, got %d jobs", len(firstResp.Jobs))
	}

	// Agent acknowledges (job A is now in-flight).
	resp = h.agentRequest(client, "POST", "/v1/agents/jobs/"+jobA+"/acknowledge", nil)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Server-side: queue job B while session is still nominally active.
	payloadB, _ := json.Marshal(protocol.ExecPayload{Command: "echo B"})
	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "exec",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payloadB),
	})
	assertStatus(t, resp, http.StatusCreated)
	var createB struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createB)
	jobB := createB.JobIDs[0]

	// Session expires on the agent. The in-flight job A must still be able to
	// finish reporting back to the server.
	now := time.Now().UTC()
	exitCode := 0
	resp = h.agentRequest(client, "POST", "/v1/agents/jobs/"+jobA+"/result", protocol.JobResultSubmission{
		Status:      "completed",
		ExitCode:    &exitCode,
		Stdout:      "A\n",
		StartedAt:   &now,
		CompletedAt: &now,
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("in-flight job result submission must succeed after CDM session expiry; got status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify job A landed in `completed`.
	var statusA string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobA).Scan(&statusA); err != nil {
		t.Fatalf("query job A: %v", err)
	}
	if statusA != models.JobStatusCompleted {
		t.Errorf("job A status = %q, want %q", statusA, models.JobStatusCompleted)
	}

	// Next checkin: agent reports the expired session.
	checkin.Sequence = 2
	checkin.Status.CDMSessionActive = false
	checkin.Status.CDMSessionExpiresAt = nil

	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)
	var secondResp protocol.CheckinResponse
	readJSON(t, resp, &secondResp)

	if len(secondResp.Jobs) != 0 {
		t.Errorf("expected 0 jobs after CDM session expiry, got %d", len(secondResp.Jobs))
	}

	// Job B should now be parked in CDM_HOLD.
	var statusB string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobB).Scan(&statusB); err != nil {
		t.Fatalf("query job B: %v", err)
	}
	if statusB != models.JobStatusCDMHold {
		t.Errorf("job B status = %q, want %q", statusB, models.JobStatusCDMHold)
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
