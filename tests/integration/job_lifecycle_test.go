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

// 20.2 — Job lifecycle end-to-end

func TestJobLifecycle_ExecJob(t *testing.T) {
	h := newHarness(t)

	// Enroll agent
	agentID, certPEM, keyPEM := h.enrollAgent("job-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Create an exec job targeting the device
	payload, _ := json.Marshal(protocol.ExecPayload{
		Command:        "echo hello",
		TimeoutSeconds: 30,
	})
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
	if len(createResp.JobIDs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(createResp.JobIDs))
	}
	jobID := createResp.JobIDs[0]

	// Verify job is queued
	var status string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusQueued {
		t.Fatalf("job status = %q, want %q", status, models.JobStatusQueued)
	}

	// Agent checks in — should receive the job
	checkinReq := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkinReq)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	if len(checkinResp.Jobs) != 1 {
		t.Fatalf("expected 1 dispatched job, got %d", len(checkinResp.Jobs))
	}
	if checkinResp.Jobs[0].JobID != jobID {
		t.Errorf("dispatched job ID = %q, want %q", checkinResp.Jobs[0].JobID, jobID)
	}
	if checkinResp.Jobs[0].Type != "exec" {
		t.Errorf("job type = %q, want %q", checkinResp.Jobs[0].Type, "exec")
	}

	// Verify job is now DISPATCHED
	err = h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusDispatched {
		t.Fatalf("job status = %q, want %q", status, models.JobStatusDispatched)
	}

	// Agent acknowledges
	resp = h.agentRequest(client, "POST", "/v1/agents/jobs/"+jobID+"/acknowledge", nil)
	assertStatus(t, resp, http.StatusNoContent)

	// Agent submits result
	now := time.Now().UTC()
	exitCode := 0
	result := protocol.JobResultSubmission{
		Status:      "completed",
		ExitCode:    &exitCode,
		Stdout:      "hello\n",
		StartedAt:   &now,
		CompletedAt: &now,
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/jobs/"+jobID+"/result", result)
	assertStatus(t, resp, http.StatusNoContent)

	// Verify job is completed
	err = h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusCompleted {
		t.Fatalf("job status = %q, want %q", status, models.JobStatusCompleted)
	}

	// Verify result stored
	var resultStdout string
	err = h.pool.QueryRow(context.Background(),
		`SELECT stdout FROM job_results WHERE job_id = $1`, jobID,
	).Scan(&resultStdout)
	if err != nil {
		t.Fatalf("query result: %v", err)
	}
	if resultStdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", resultStdout, "hello\n")
	}
}

func TestJobLifecycle_Retry(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("retry-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Create job with retry policy
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "fail-cmd"})
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":         "exec",
		"target":       map[string]any{"device_ids": []string{agentID}},
		"payload":      json.RawMessage(payload),
		"retry_policy": map[string]any{"max_retries": 2, "retry_delay_seconds": 0},
	})
	assertStatus(t, resp, http.StatusCreated)

	var createResp struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &createResp)
	jobID := createResp.JobIDs[0]

	// Agent receives job
	checkinReq := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkinReq)
	assertStatus(t, resp, http.StatusOK)
	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)
	if len(checkinResp.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(checkinResp.Jobs))
	}

	// Agent acknowledges and reports failure
	resp = h.agentRequest(client, "POST", "/v1/agents/jobs/"+jobID+"/acknowledge", nil)
	assertStatus(t, resp, http.StatusNoContent)

	now := time.Now().UTC()
	exitCode := 1
	resp = h.agentRequest(client, "POST", "/v1/agents/jobs/"+jobID+"/result", protocol.JobResultSubmission{
		Status:      "failed",
		ExitCode:    &exitCode,
		Stderr:      "command not found",
		StartedAt:   &now,
		CompletedAt: &now,
	})
	assertStatus(t, resp, http.StatusNoContent)

	// Original job should be failed, and a retry job should exist
	var origStatus string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID,
	).Scan(&origStatus)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if origStatus != models.JobStatusFailed {
		t.Fatalf("original job status = %q, want %q", origStatus, models.JobStatusFailed)
	}

	// Check for retry job (linked via parent_job_id)
	var retryCount int
	err = h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM jobs WHERE parent_job_id = $1`, jobID,
	).Scan(&retryCount)
	if err != nil {
		t.Fatalf("query retries: %v", err)
	}
	if retryCount != 1 {
		t.Errorf("retry job count = %d, want 1", retryCount)
	}
}

func TestJobLifecycle_Cancel(t *testing.T) {
	h := newHarness(t)

	agentID, _, _ := h.enrollAgent("cancel-host")

	// Create a job
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "sleep 60"})
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

	// Cancel it before agent picks it up
	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs/"+jobID+"/cancel", nil)
	assertStatus(t, resp, http.StatusNoContent)

	// Verify status
	var status string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM jobs WHERE id = $1`, jobID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != models.JobStatusCancelled {
		t.Errorf("status = %q, want %q", status, models.JobStatusCancelled)
	}
}

func TestJobLifecycle_NotDispatchedAfterCancel(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("no-dispatch-host")

	// Create + cancel a job
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo never"})
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

	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs/"+createResp.JobIDs[0]+"/cancel", nil)
	assertStatus(t, resp, http.StatusNoContent)

	// Agent checks in — should receive no jobs
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	checkinReq := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkinReq)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	if len(checkinResp.Jobs) != 0 {
		t.Errorf("expected 0 jobs after cancel, got %d", len(checkinResp.Jobs))
	}
}
