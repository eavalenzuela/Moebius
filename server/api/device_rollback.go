package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// DeviceRollbackHandler handles POST /v1/devices/{device_id}/rollback.
type DeviceRollbackHandler struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
}

// NewDeviceRollbackHandler creates a DeviceRollbackHandler.
func NewDeviceRollbackHandler(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *DeviceRollbackHandler {
	return &DeviceRollbackHandler{pool: pool, audit: auditLog, log: log}
}

// Rollback handles POST /v1/devices/{device_id}/rollback.
func (h *DeviceRollbackHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	deviceID := chi.URLParam(r, "device_id")

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Verify device exists and belongs to tenant
	var exists bool
	err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM devices WHERE id = $1 AND tenant_id = $2)`,
		deviceID, tenantID,
	).Scan(&exists)
	if err != nil {
		h.log.Error("check device", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to verify device")
		return
	}
	if !exists {
		Error(w, http.StatusNotFound, "device not found")
		return
	}

	// Create agent_rollback job
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]string{"reason": req.Reason})

	jobID := models.NewJobID()
	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO jobs (id, tenant_id, device_id, type, status, payload, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		jobID, tenantID, deviceID, models.JobTypeAgentRollback,
		models.JobStatusQueued, payload, userID, now,
	)
	if err != nil {
		h.log.Error("create rollback job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create rollback job")
		return
	}

	_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"device.rollback", "device", deviceID, map[string]any{
			"job_id": jobID,
			"reason": req.Reason,
		})

	JSON(w, http.StatusCreated, map[string]string{
		"job_id":    jobID,
		"device_id": deviceID,
		"type":      models.JobTypeAgentRollback,
		"status":    models.JobStatusQueued,
	})
}
