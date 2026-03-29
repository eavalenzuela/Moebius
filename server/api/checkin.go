package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moebius-oss/moebius/server/audit"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/shared/models"
	"github.com/moebius-oss/moebius/shared/protocol"
)

// CheckinHandler handles POST /v1/agents/checkin.
type CheckinHandler struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
}

// NewCheckinHandler creates a CheckinHandler.
func NewCheckinHandler(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *CheckinHandler {
	return &CheckinHandler{
		pool:  pool,
		audit: auditLog,
		log:   log,
	}
}

// ServeHTTP handles agent check-in requests.
func (h *CheckinHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	agentID := auth.AgentIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	if agentID == "" || tenantID == "" {
		Error(w, http.StatusUnauthorized, "agent identity not found")
		return
	}

	var req protocol.CheckinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()

	// Update device record
	_, err := h.pool.Exec(ctx,
		`UPDATE devices SET
			last_seen_at = $1,
			status = $2,
			agent_version = $3,
			cdm_enabled = $4,
			cdm_session_active = $5,
			cdm_session_expires_at = $6,
			sequence_last = $7
		 WHERE id = $8 AND tenant_id = $9`,
		now,
		models.DeviceStatusOnline,
		req.Status.AgentVersion,
		req.Status.CDMEnabled,
		req.Status.CDMSessionActive,
		req.Status.CDMSessionExpiresAt,
		req.Sequence,
		agentID,
		tenantID,
	)
	if err != nil {
		h.log.Error("failed to update device", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to update device")
		return
	}

	// Process inventory delta if present
	if req.InventoryDelta != nil && req.InventoryDelta.Packages != nil {
		if err := h.processPackageDelta(ctx, agentID, req.InventoryDelta.Packages); err != nil {
			h.log.Error("failed to process inventory delta", slog.String("error", err.Error()))
			// Non-fatal — continue
		}
	}

	// Auto-requeue dispatched jobs that the agent missed (DISPATCHED → QUEUED)
	_, err = h.pool.Exec(ctx,
		`UPDATE jobs SET status = $1, dispatched_at = NULL
		 WHERE device_id = $2 AND tenant_id = $3
		   AND status = $4
		   AND dispatched_at < $5`,
		models.JobStatusQueued,
		agentID, tenantID,
		models.JobStatusDispatched,
		now.Add(-60*time.Second),
	)
	if err != nil {
		h.log.Error("failed to requeue stale jobs", slog.String("error", err.Error()))
		// Non-fatal — continue
	}

	// Fetch dispatchable jobs
	jobs, err := h.fetchDispatchableJobs(ctx, agentID, tenantID, req.Status.CDMEnabled, req.Status.CDMSessionActive)
	if err != nil {
		h.log.Error("failed to fetch jobs", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to fetch jobs")
		return
	}

	// Mark fetched jobs as DISPATCHED
	var dispatched []protocol.JobDispatch
	for _, j := range jobs {
		_, err := h.pool.Exec(ctx,
			`UPDATE jobs SET status = $1, dispatched_at = $2 WHERE id = $3`,
			models.JobStatusDispatched, now, j.ID,
		)
		if err != nil {
			h.log.Error("failed to mark job dispatched",
				slog.String("job_id", j.ID), slog.String("error", err.Error()))
			continue
		}
		dispatched = append(dispatched, protocol.JobDispatch{
			JobID:     j.ID,
			Type:      j.Type,
			Payload:   j.Payload,
			CreatedAt: j.CreatedAt,
		})
	}

	resp := protocol.CheckinResponse{
		Timestamp: now,
		Jobs:      dispatched,
	}

	JSON(w, http.StatusOK, resp)
}

// fetchDispatchableJobs queries for QUEUED jobs, applying CDM logic.
func (h *CheckinHandler) fetchDispatchableJobs(
	ctx context.Context,
	agentID, tenantID string,
	cdmEnabled, cdmSessionActive bool,
) ([]models.Job, error) {
	if cdmEnabled && !cdmSessionActive {
		// CDM active but no session — hold all queued jobs
		_, err := h.pool.Exec(ctx,
			`UPDATE jobs SET status = $1
			 WHERE device_id = $2 AND tenant_id = $3 AND status = $4`,
			models.JobStatusCDMHold,
			agentID, tenantID,
			models.JobStatusQueued,
		)
		return nil, err
	}

	// If CDM session just became active, release held jobs back to queued
	if cdmEnabled && cdmSessionActive {
		_, _ = h.pool.Exec(ctx,
			`UPDATE jobs SET status = $1
			 WHERE device_id = $2 AND tenant_id = $3 AND status = $4`,
			models.JobStatusQueued,
			agentID, tenantID,
			models.JobStatusCDMHold,
		)
	}

	// Fetch queued jobs (limit to 10 per check-in to avoid overloading agent)
	rows, err := h.pool.Query(ctx,
		`SELECT id, tenant_id, device_id, type, status, payload, created_at
		 FROM jobs
		 WHERE device_id = $1 AND tenant_id = $2 AND status = $3
		 ORDER BY created_at ASC
		 LIMIT 10`,
		agentID, tenantID, models.JobStatusQueued,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.TenantID, &j.DeviceID, &j.Type, &j.Status, &j.Payload, &j.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}

// processPackageDelta applies a package delta from the agent check-in.
func (h *CheckinHandler) processPackageDelta(ctx context.Context, deviceID string, delta *protocol.PackageDelta) error {
	now := time.Now().UTC()

	// Added packages
	for _, p := range delta.Added {
		_, err := h.pool.Exec(ctx,
			`INSERT INTO inventory_packages (id, device_id, name, version, manager, last_seen_at)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT DO NOTHING`,
			models.NewInventoryPkgID(), deviceID, p.Name, p.Version, p.Manager, now,
		)
		if err != nil {
			return fmt.Errorf("insert package %s: %w", p.Name, err)
		}
	}

	// Updated packages
	for _, p := range delta.Updated {
		_, err := h.pool.Exec(ctx,
			`UPDATE inventory_packages
			 SET version = $1, last_seen_at = $2
			 WHERE device_id = $3 AND name = $4 AND manager = $5`,
			p.Version, now, deviceID, p.Name, p.Manager,
		)
		if err != nil {
			return fmt.Errorf("update package %s: %w", p.Name, err)
		}
	}

	// Removed packages
	for _, p := range delta.Removed {
		_, err := h.pool.Exec(ctx,
			`DELETE FROM inventory_packages
			 WHERE device_id = $1 AND name = $2 AND manager = $3`,
			deviceID, p.Name, p.Manager,
		)
		if err != nil {
			return fmt.Errorf("delete package %s: %w", p.Name, err)
		}
	}

	return nil
}
