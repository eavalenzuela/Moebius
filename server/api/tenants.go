package api

import (
	"encoding/json"
	"net/http"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// TenantHandler handles /v1/tenant endpoints.
type TenantHandler struct {
	store *store.Store
	audit *audit.Logger
}

// NewTenantHandler creates a TenantHandler.
func NewTenantHandler(s *store.Store, auditLog *audit.Logger) *TenantHandler {
	return &TenantHandler{store: s, audit: auditLog}
}

// Get handles GET /v1/tenant.
func (h *TenantHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	tenant, err := h.store.GetTenant(r.Context(), tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get tenant")
		return
	}
	if tenant == nil {
		ErrorWithCode(w, http.StatusNotFound, "tenant_not_found", "Tenant not found")
		return
	}
	JSON(w, http.StatusOK, tenant)
}

type updateTenantRequest struct {
	Name   string               `json:"name,omitempty"`
	Config *models.TenantConfig `json:"config,omitempty"`
}

// Update handles PATCH /v1/tenant.
func (h *TenantHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	tenant, err := h.store.GetTenant(r.Context(), tenantID)
	if err != nil || tenant == nil {
		Error(w, http.StatusInternalServerError, "failed to get tenant")
		return
	}

	var req updateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != "" {
		tenant.Name = req.Name
	}
	if req.Config != nil {
		tenant.Config = req.Config
	}

	if err := h.store.UpdateTenant(r.Context(), tenant); err != nil {
		Error(w, http.StatusInternalServerError, "failed to update tenant")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"tenant.update", "tenant", tenantID, nil)
	}

	JSON(w, http.StatusOK, tenant)
}
