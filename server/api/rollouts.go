package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// RolloutsHandler manages gradual rollout operations.
type RolloutsHandler struct {
	store *store.Store
	audit *audit.Logger
	log   *slog.Logger
}

// NewRolloutsHandler creates a RolloutsHandler.
func NewRolloutsHandler(st *store.Store, auditLog *audit.Logger, log *slog.Logger) *RolloutsHandler {
	return &RolloutsHandler{store: st, audit: auditLog, log: log}
}

// GetStatus handles GET /v1/agent-versions/{version}/rollout.
func (h *RolloutsHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ver := chi.URLParam(r, "version")

	// Look up version to get its ID
	v, err := h.store.GetAgentVersion(r.Context(), ver)
	if err != nil {
		h.log.Error("get agent version for rollout", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to look up version")
		return
	}
	if v == nil {
		Error(w, http.StatusNotFound, "version not found")
		return
	}

	rollout, err := h.store.GetRollout(r.Context(), v.ID, tenantID)
	if err != nil {
		h.log.Error("get rollout", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get rollout")
		return
	}
	if rollout == nil {
		Error(w, http.StatusNotFound, "no rollout found for this version")
		return
	}

	JSON(w, http.StatusOK, rollout)
}

// Pause handles POST /v1/agent-versions/{version}/rollout/pause.
func (h *RolloutsHandler) Pause(w http.ResponseWriter, r *http.Request) {
	h.updateRolloutStatus(w, r, models.RolloutStatusPaused, "rollout.pause")
}

// Resume handles POST /v1/agent-versions/{version}/rollout/resume.
func (h *RolloutsHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.updateRolloutStatus(w, r, models.RolloutStatusInProgress, "rollout.resume")
}

// Abort handles POST /v1/agent-versions/{version}/rollout/abort.
func (h *RolloutsHandler) Abort(w http.ResponseWriter, r *http.Request) {
	h.updateRolloutStatus(w, r, models.RolloutStatusAborted, "rollout.abort")
}

func (h *RolloutsHandler) updateRolloutStatus(w http.ResponseWriter, r *http.Request, newStatus, action string) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ver := chi.URLParam(r, "version")

	v, err := h.store.GetAgentVersion(r.Context(), ver)
	if err != nil {
		h.log.Error("get agent version for rollout", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to look up version")
		return
	}
	if v == nil {
		Error(w, http.StatusNotFound, "version not found")
		return
	}

	rollout, err := h.store.GetRollout(r.Context(), v.ID, tenantID)
	if err != nil {
		h.log.Error("get rollout", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get rollout")
		return
	}
	if rollout == nil {
		Error(w, http.StatusNotFound, "no rollout found for this version")
		return
	}

	// Validate state transitions
	switch newStatus {
	case models.RolloutStatusPaused:
		if rollout.Status != models.RolloutStatusInProgress {
			ErrorWithCode(w, http.StatusConflict, "invalid_state", "can only pause an in-progress rollout")
			return
		}
	case models.RolloutStatusInProgress:
		if rollout.Status != models.RolloutStatusPaused {
			ErrorWithCode(w, http.StatusConflict, "invalid_state", "can only resume a paused rollout")
			return
		}
	case models.RolloutStatusAborted:
		if rollout.Status != models.RolloutStatusInProgress && rollout.Status != models.RolloutStatusPaused {
			ErrorWithCode(w, http.StatusConflict, "invalid_state", "can only abort an active or paused rollout")
			return
		}
	}

	if err := h.store.UpdateRolloutStatus(r.Context(), rollout.ID, newStatus); err != nil {
		h.log.Error("update rollout status", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to update rollout")
		return
	}

	_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		action, "rollout", rollout.ID, map[string]any{
			"version":    ver,
			"new_status": newStatus,
		})

	w.WriteHeader(http.StatusNoContent)
}
