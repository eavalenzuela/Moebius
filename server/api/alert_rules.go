package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// AlertRulesHandler handles /v1/alert-rules endpoints.
type AlertRulesHandler struct {
	store *store.Store
	audit *audit.Logger
	log   *slog.Logger
}

// NewAlertRulesHandler creates an AlertRulesHandler.
func NewAlertRulesHandler(st *store.Store, auditLog *audit.Logger, log *slog.Logger) *AlertRulesHandler {
	return &AlertRulesHandler{store: st, audit: auditLog, log: log}
}

type createAlertRuleRequest struct {
	Name      string                `json:"name"`
	Condition json.RawMessage       `json:"condition"`
	Channels  *models.AlertChannels `json:"channels"`
	Enabled   *bool                 `json:"enabled,omitempty"`
}

// Create handles POST /v1/alert-rules.
func (h *AlertRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req createAlertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Condition) == 0 {
		Error(w, http.StatusBadRequest, "condition is required")
		return
	}
	if req.Channels == nil {
		Error(w, http.StatusBadRequest, "channels is required")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	ar := &models.AlertRule{
		ID:        models.NewAlertRuleID(),
		TenantID:  tenantID,
		Name:      req.Name,
		Condition: req.Condition,
		Channels:  req.Channels,
		Enabled:   enabled,
	}

	if err := h.store.CreateAlertRule(r.Context(), ar); err != nil {
		h.log.Error("failed to create alert rule", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create alert rule")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"alert_rule.create", "alert_rule", ar.ID, nil)
	}

	JSON(w, http.StatusCreated, ar)
}

// List handles GET /v1/alert-rules.
func (h *AlertRulesHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	result, err := h.store.ListAlertRules(r.Context(), tenantID)
	if err != nil {
		h.log.Error("failed to list alert rules", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list alert rules")
		return
	}
	if result == nil {
		result = []models.AlertRule{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": result})
}

// Get handles GET /v1/alert-rules/{rule_id}.
func (h *AlertRulesHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	id := chi.URLParam(r, "rule_id")

	ar, err := h.store.GetAlertRule(r.Context(), tenantID, id)
	if err != nil {
		h.log.Error("failed to get alert rule", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get alert rule")
		return
	}
	if ar == nil {
		Error(w, http.StatusNotFound, "alert rule not found")
		return
	}
	JSON(w, http.StatusOK, ar)
}

type updateAlertRuleRequest struct {
	Name      *string               `json:"name,omitempty"`
	Condition json.RawMessage       `json:"condition,omitempty"`
	Channels  *models.AlertChannels `json:"channels,omitempty"`
}

// Update handles PATCH /v1/alert-rules/{rule_id}.
func (h *AlertRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "rule_id")

	var req updateAlertRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := make(map[string]any)
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Condition != nil {
		updates["condition"] = req.Condition
	}
	if req.Channels != nil {
		channelsJSON, _ := json.Marshal(req.Channels)
		updates["channels"] = channelsJSON
	}

	if len(updates) == 0 {
		Error(w, http.StatusBadRequest, "no fields to update")
		return
	}

	if err := h.store.UpdateAlertRule(r.Context(), tenantID, id, updates); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "alert rule not found")
			return
		}
		h.log.Error("failed to update alert rule", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to update alert rule")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"alert_rule.update", "alert_rule", id, nil)
	}

	ar, _ := h.store.GetAlertRule(r.Context(), tenantID, id)
	JSON(w, http.StatusOK, ar)
}

// Delete handles DELETE /v1/alert-rules/{rule_id}.
func (h *AlertRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "rule_id")

	if err := h.store.DeleteAlertRule(r.Context(), tenantID, id); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "alert rule not found")
			return
		}
		h.log.Error("failed to delete alert rule", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to delete alert rule")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"alert_rule.delete", "alert_rule", id, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Enable handles POST /v1/alert-rules/{rule_id}/enable.
func (h *AlertRulesHandler) Enable(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "rule_id")

	if err := h.store.SetAlertRuleEnabled(r.Context(), tenantID, id, true); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "alert rule not found")
			return
		}
		h.log.Error("failed to enable alert rule", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to enable alert rule")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"alert_rule.enable", "alert_rule", id, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Disable handles POST /v1/alert-rules/{rule_id}/disable.
func (h *AlertRulesHandler) Disable(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "rule_id")

	if err := h.store.SetAlertRuleEnabled(r.Context(), tenantID, id, false); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "alert rule not found")
			return
		}
		h.log.Error("failed to disable alert rule", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to disable alert rule")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"alert_rule.disable", "alert_rule", id, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}
