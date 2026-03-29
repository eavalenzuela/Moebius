package api

import (
	"encoding/json"
	"net/http"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
)

// RolesHandler handles /v1/roles endpoints.
type RolesHandler struct {
	store *store.Store
}

// NewRolesHandler creates a RolesHandler.
func NewRolesHandler(s *store.Store) *RolesHandler {
	return &RolesHandler{store: s}
}

// List handles GET /v1/roles.
func (h *RolesHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	roles, err := h.store.ListRoles(r.Context(), tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list roles")
		return
	}
	if roles == nil {
		roles = []models.Role{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": roles})
}

// Get handles GET /v1/roles/{role_id}.
func (h *RolesHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	roleID := chi.URLParam(r, "role_id")

	role, err := h.store.GetRole(r.Context(), tenantID, roleID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get role")
		return
	}
	if role == nil {
		ErrorWithCode(w, http.StatusNotFound, "role_not_found", "No role with the given ID exists")
		return
	}
	JSON(w, http.StatusOK, role)
}

type createRoleRequest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

// Create handles POST /v1/roles.
func (h *RolesHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	var req createRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || len(req.Permissions) == 0 {
		Error(w, http.StatusBadRequest, "name and permissions are required")
		return
	}

	role := &models.Role{
		ID:          models.NewRoleID(),
		TenantID:    tenantID,
		Name:        req.Name,
		Permissions: req.Permissions,
		IsCustom:    true,
	}

	if err := h.store.CreateRole(r.Context(), role); err != nil {
		Error(w, http.StatusInternalServerError, "failed to create role")
		return
	}

	JSON(w, http.StatusCreated, role)
}

type updateRoleRequest struct {
	Name        string   `json:"name,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// Update handles PATCH /v1/roles/{role_id}.
func (h *RolesHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	roleID := chi.URLParam(r, "role_id")

	// Fetch current role
	existing, err := h.store.GetRole(r.Context(), tenantID, roleID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get role")
		return
	}
	if existing == nil {
		ErrorWithCode(w, http.StatusNotFound, "role_not_found", "No role with the given ID exists")
		return
	}
	if !existing.IsCustom {
		ErrorWithCode(w, http.StatusForbidden, "system_role_immutable", "System roles cannot be modified")
		return
	}

	var req updateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if len(req.Permissions) > 0 {
		existing.Permissions = req.Permissions
	}

	if err := h.store.UpdateRole(r.Context(), tenantID, existing); err != nil {
		Error(w, http.StatusInternalServerError, "failed to update role")
		return
	}

	JSON(w, http.StatusOK, existing)
}

// Delete handles DELETE /v1/roles/{role_id}.
func (h *RolesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	roleID := chi.URLParam(r, "role_id")

	if err := h.store.DeleteRole(r.Context(), tenantID, roleID); err != nil {
		ErrorWithCode(w, http.StatusConflict, "role_in_use", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
