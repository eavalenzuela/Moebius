package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// UpdatePoliciesHandler manages agent auto-update policies.
type UpdatePoliciesHandler struct {
	store *store.Store
	audit *audit.Logger
	log   *slog.Logger
}

// NewUpdatePoliciesHandler creates an UpdatePoliciesHandler.
func NewUpdatePoliciesHandler(st *store.Store, auditLog *audit.Logger, log *slog.Logger) *UpdatePoliciesHandler {
	return &UpdatePoliciesHandler{store: st, audit: auditLog, log: log}
}

type upsertPolicyRequest struct {
	GroupID                     string `json:"group_id,omitempty"`
	Enabled                     *bool  `json:"enabled"`
	Channel                     string `json:"channel"`
	Schedule                    string `json:"schedule,omitempty"`
	RolloutStrategy             string `json:"rollout_strategy"`
	RolloutBatchPercent         *int   `json:"rollout_batch_percent,omitempty"`
	RolloutBatchIntervalMinutes *int   `json:"rollout_batch_interval_minutes,omitempty"`
}

// Upsert handles POST /v1/update-policies.
func (h *UpdatePoliciesHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req upsertPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Channel == "" {
		req.Channel = models.ChannelStable
	}
	if req.Channel != models.ChannelStable && req.Channel != models.ChannelBeta && req.Channel != models.ChannelCanary {
		Error(w, http.StatusBadRequest, "channel must be stable, beta, or canary")
		return
	}
	if req.RolloutStrategy == "" {
		req.RolloutStrategy = "gradual"
	}
	if req.RolloutStrategy != "immediate" && req.RolloutStrategy != "gradual" {
		Error(w, http.StatusBadRequest, "rollout_strategy must be immediate or gradual")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	batchPct := 10
	if req.RolloutBatchPercent != nil {
		batchPct = *req.RolloutBatchPercent
	}
	batchInterval := 60
	if req.RolloutBatchIntervalMinutes != nil {
		batchInterval = *req.RolloutBatchIntervalMinutes
	}

	p := models.AgentUpdatePolicy{
		ID:                          models.NewUpdatePolicyID(),
		TenantID:                    tenantID,
		GroupID:                     req.GroupID,
		Enabled:                     enabled,
		Channel:                     req.Channel,
		Schedule:                    req.Schedule,
		RolloutStrategy:             req.RolloutStrategy,
		RolloutBatchPercent:         batchPct,
		RolloutBatchIntervalMinutes: batchInterval,
	}

	if err := h.store.UpsertUpdatePolicy(r.Context(), &p); err != nil {
		h.log.Error("upsert update policy", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to save policy")
		return
	}

	_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"update_policy.upsert", "update_policy", p.ID, map[string]any{
			"group_id": p.GroupID,
			"channel":  p.Channel,
		})

	JSON(w, http.StatusOK, p)
}

// List handles GET /v1/update-policies.
func (h *UpdatePoliciesHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	policies, err := h.store.ListUpdatePolicies(r.Context(), tenantID)
	if err != nil {
		h.log.Error("list update policies", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list policies")
		return
	}
	if policies == nil {
		policies = []models.AgentUpdatePolicy{}
	}

	JSON(w, http.StatusOK, map[string]any{"data": policies})
}

// Delete handles DELETE /v1/update-policies/{policy_id}.
func (h *UpdatePoliciesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	policyID := chi.URLParam(r, "policy_id")

	if err := h.store.DeleteUpdatePolicy(r.Context(), tenantID, policyID); err != nil {
		h.log.Error("delete update policy", slog.String("error", err.Error()))
		Error(w, http.StatusNotFound, "policy not found")
		return
	}

	_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"update_policy.delete", "update_policy", policyID, nil)

	w.WriteHeader(http.StatusNoContent)
}
