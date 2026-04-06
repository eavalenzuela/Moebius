package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/jobs"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentJobsHandler handles agent-facing job endpoints (mTLS-authenticated).
type AgentJobsHandler struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
}

// NewAgentJobsHandler creates an AgentJobsHandler.
func NewAgentJobsHandler(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *AgentJobsHandler {
	return &AgentJobsHandler{
		pool:  pool,
		audit: auditLog,
		log:   log,
	}
}

// Acknowledge handles POST /v1/agents/jobs/{job_id}/acknowledge.
func (h *AgentJobsHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	agentID := auth.AgentIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	jobID := chi.URLParam(r, "job_id")
	ctx := r.Context()
	now := time.Now().UTC()

	// Fetch current job status
	var currentStatus string
	err := h.pool.QueryRow(ctx,
		`SELECT status FROM jobs WHERE id = $1 AND device_id = $2 AND tenant_id = $3`,
		jobID, agentID, tenantID,
	).Scan(&currentStatus)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("failed to get job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	if err := jobs.ValidateTransition(currentStatus, models.JobStatusAcknowledged); err != nil {
		ErrorWithCode(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}

	_, err = h.pool.Exec(ctx,
		`UPDATE jobs SET status = $1, acknowledged_at = $2 WHERE id = $3`,
		models.JobStatusAcknowledged, now, jobID,
	)
	if err != nil {
		h.log.Error("failed to acknowledge job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to acknowledge job")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(ctx, tenantID, agentID, models.ActorTypeAgent,
			"job.acknowledge", "job", jobID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

// SubmitResult handles POST /v1/agents/jobs/{job_id}/result.
func (h *AgentJobsHandler) SubmitResult(w http.ResponseWriter, r *http.Request) {
	agentID := auth.AgentIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	jobID := chi.URLParam(r, "job_id")
	ctx := r.Context()
	now := time.Now().UTC()

	var req protocol.JobResultSubmission
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Map submitted status to job status
	var targetStatus string
	switch req.Status {
	case "completed":
		targetStatus = models.JobStatusCompleted
	case "failed":
		targetStatus = models.JobStatusFailed
	case "timed_out":
		targetStatus = models.JobStatusTimedOut
	default:
		Error(w, http.StatusBadRequest, "invalid status: must be completed, failed, or timed_out")
		return
	}

	// Fetch current job state
	var currentStatus, jobType string
	var retryCount, maxRetries int
	err := h.pool.QueryRow(ctx,
		`SELECT status, type, retry_count, max_retries FROM jobs
		 WHERE id = $1 AND device_id = $2 AND tenant_id = $3`,
		jobID, agentID, tenantID,
	).Scan(&currentStatus, &jobType, &retryCount, &maxRetries)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("failed to get job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	// The agent can submit results from ACKNOWLEDGED or RUNNING state.
	// First transition to RUNNING if currently acknowledged.
	if currentStatus == models.JobStatusAcknowledged {
		currentStatus = models.JobStatusRunning
	}

	if err := jobs.ValidateTransition(currentStatus, targetStatus); err != nil {
		ErrorWithCode(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}

	// Store the result
	resultID := models.NewJobResultID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO job_results (id, job_id, exit_code, stdout, stderr, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		resultID, jobID, req.ExitCode, req.Stdout, req.Stderr, req.StartedAt, req.CompletedAt,
	)
	if err != nil {
		h.log.Error("failed to store result", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store result")
		return
	}

	// Update job to terminal status
	lastError := req.Message
	_, err = h.pool.Exec(ctx,
		`UPDATE jobs SET status = $1, completed_at = $2, last_error = $3, started_at = COALESCE(started_at, $4)
		 WHERE id = $5`,
		targetStatus, now, lastError, req.StartedAt, jobID,
	)
	if err != nil {
		h.log.Error("failed to update job status", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to update job")
		return
	}

	// Process full inventory result if applicable
	if jobType == models.JobTypeInventoryFull && targetStatus == models.JobStatusCompleted {
		if err := h.processFullInventory(ctx, agentID, req.Stdout); err != nil {
			h.log.Error("failed to process full inventory", slog.String("error", err.Error()))
			// Non-fatal — the job result is already stored
		}
	}

	if h.audit != nil {
		_ = h.audit.LogAction(ctx, tenantID, agentID, models.ActorTypeAgent,
			"job.result", "job", jobID, map[string]string{
				"status": targetStatus,
			})
	}

	// Handle auto-retry
	if jobs.ShouldRetry(targetStatus, retryCount, maxRetries) {
		h.createRetryJob(ctx, jobID, tenantID, agentID, jobType, retryCount+1, maxRetries)
	}

	w.WriteHeader(http.StatusNoContent)
}

// processFullInventory stores hardware and package data from an inventory_full job result.
func (h *AgentJobsHandler) processFullInventory(ctx context.Context, deviceID, resultData string) error {
	var inv struct {
		Hardware *fullInventoryHW `json:"hardware,omitempty"`
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Manager string `json:"manager"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(resultData), &inv); err != nil {
		return fmt.Errorf("unmarshal inventory: %w", err)
	}

	now := time.Now().UTC()

	// Upsert hardware: delete old, insert new
	if inv.Hardware != nil {
		_, _ = h.pool.Exec(ctx, `DELETE FROM inventory_hardware WHERE device_id = $1`, deviceID)

		cpuJSON, _ := json.Marshal(inv.Hardware.CPU)
		disksJSON, _ := json.Marshal(inv.Hardware.Disks)
		nicsJSON, _ := json.Marshal(inv.Hardware.NetworkInterfaces)

		_, err := h.pool.Exec(ctx,
			`INSERT INTO inventory_hardware (id, device_id, collected_at, cpu, ram_mb, disks, network_interfaces)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			models.NewInventoryHWID(), deviceID, now, cpuJSON, inv.Hardware.RAMMB, disksJSON, nicsJSON,
		)
		if err != nil {
			return fmt.Errorf("insert hardware: %w", err)
		}
	}

	// Replace all packages: delete old, insert new
	if len(inv.Packages) > 0 {
		_, _ = h.pool.Exec(ctx, `DELETE FROM inventory_packages WHERE device_id = $1`, deviceID)

		for _, p := range inv.Packages {
			_, err := h.pool.Exec(ctx,
				`INSERT INTO inventory_packages (id, device_id, name, version, manager, last_seen_at)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				models.NewInventoryPkgID(), deviceID, p.Name, p.Version, p.Manager, now,
			)
			if err != nil {
				return fmt.Errorf("insert package %s: %w", p.Name, err)
			}
		}
	}

	return nil
}

type fullInventoryHW struct {
	CPU               json.RawMessage `json:"cpu,omitempty"`
	RAMMB             int64           `json:"ram_mb,omitempty"`
	Disks             json.RawMessage `json:"disks,omitempty"`
	NetworkInterfaces json.RawMessage `json:"network_interfaces,omitempty"`
}

// createRetryJob creates a new job record linked to the parent for retry.
func (h *AgentJobsHandler) createRetryJob(ctx context.Context, parentJobID, tenantID, deviceID, jobType string, retryCount, maxRetries int) {
	now := time.Now().UTC()

	// Copy payload and retry_policy from parent
	var payload json.RawMessage
	var retryPolicy []byte
	var createdBy *string
	err := h.pool.QueryRow(ctx,
		`SELECT payload, retry_policy, created_by FROM jobs WHERE id = $1`,
		parentJobID,
	).Scan(&payload, &retryPolicy, &createdBy)
	if err != nil {
		h.log.Error("failed to read parent job for retry", slog.String("error", err.Error()))
		return
	}

	retryJobID := models.NewJobID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO jobs (id, tenant_id, device_id, parent_job_id, type, status, payload,
						   retry_policy, retry_count, max_retries, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		retryJobID, tenantID, deviceID, parentJobID, jobType, models.JobStatusQueued,
		payload, retryPolicy, retryCount, maxRetries, createdBy, now,
	)
	if err != nil {
		h.log.Error("failed to create retry job", slog.String("error", err.Error()))
		return
	}

	h.log.Info("retry job created",
		slog.String("parent_job_id", parentJobID),
		slog.String("retry_job_id", retryJobID),
		slog.Int("retry_count", retryCount),
	)
}
