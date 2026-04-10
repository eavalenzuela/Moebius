package api

import (
	"encoding/json"
	"net/http"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
)

// GroupsHandler handles /v1/groups endpoints.
type GroupsHandler struct {
	store *store.Store
	audit *audit.Logger
}

// NewGroupsHandler creates a GroupsHandler.
func NewGroupsHandler(s *store.Store, auditLog *audit.Logger) *GroupsHandler {
	return &GroupsHandler{store: s, audit: auditLog}
}

func (h *GroupsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	groups, err := h.store.ListGroups(ctx, tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	if groups == nil {
		groups = []models.Group{}
	}

	// Scope enforcement: filter to scoped groups if GroupIDs is set
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if auth.ScopeHasField(scope, "groups") {
			var filtered []models.Group
			for _, g := range groups {
				if auth.IDInScopeField(scope, "groups", g.ID) {
					filtered = append(filtered, g)
				}
			}
			groups = filtered
			if groups == nil {
				groups = []models.Group{}
			}
		}
	}

	JSON(w, http.StatusOK, map[string]any{"data": groups})
}

func (h *GroupsHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	groupID := chi.URLParam(r, "group_id")

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if !auth.IDInScopeField(scope, "groups", groupID) {
			Error(w, http.StatusNotFound, "group not found")
			return
		}
	}

	group, err := h.store.GetGroup(ctx, tenantID, groupID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get group")
		return
	}
	if group == nil {
		Error(w, http.StatusNotFound, "group not found")
		return
	}
	JSON(w, http.StatusOK, group)
}

type createGroupRequest struct {
	Name string `json:"name"`
}

func (h *GroupsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()

	// Scope enforcement: scoped keys cannot create new groups
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if auth.ScopeHasField(scope, "groups") {
			ErrorWithCode(w, http.StatusForbidden, "scope_violation", "scoped keys cannot create new groups")
			return
		}
	}

	var req createGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	g := &models.Group{
		ID:       models.NewGroupID(),
		TenantID: tenantID,
		Name:     req.Name,
	}
	if err := h.store.CreateGroup(r.Context(), g); err != nil {
		Error(w, http.StatusInternalServerError, "failed to create group")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"group.create", "group", g.ID, map[string]string{"name": req.Name})
	}

	JSON(w, http.StatusCreated, g)
}

func (h *GroupsHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	groupID := chi.URLParam(r, "group_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "groups", groupID) {
			Error(w, http.StatusNotFound, "group not found")
			return
		}
	}

	var req createGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := h.store.UpdateGroup(r.Context(), tenantID, groupID, req.Name); err != nil {
		Error(w, http.StatusNotFound, "group not found")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"group.update", "group", groupID, nil)
	}

	group, _ := h.store.GetGroup(r.Context(), tenantID, groupID)
	JSON(w, http.StatusOK, group)
}

func (h *GroupsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	groupID := chi.URLParam(r, "group_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "groups", groupID) {
			Error(w, http.StatusNotFound, "group not found")
			return
		}
	}

	if err := h.store.DeleteGroup(r.Context(), tenantID, groupID); err != nil {
		Error(w, http.StatusNotFound, "group not found")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"group.delete", "group", groupID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *GroupsHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	groupID := chi.URLParam(r, "group_id")

	devices, err := h.store.ListGroupDevices(r.Context(), tenantID, groupID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list group devices")
		return
	}
	if devices == nil {
		devices = []models.Device{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": devices})
}

type addDevicesRequest struct {
	DeviceIDs []string `json:"device_ids"`
}

func (h *GroupsHandler) AddDevices(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	groupID := chi.URLParam(r, "group_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "groups", groupID) {
			Error(w, http.StatusNotFound, "group not found")
			return
		}
	}

	var req addDevicesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.DeviceIDs) == 0 {
		Error(w, http.StatusBadRequest, "device_ids is required")
		return
	}

	if err := h.store.AddDevicesToGroup(r.Context(), tenantID, groupID, req.DeviceIDs); err != nil {
		Error(w, http.StatusInternalServerError, "failed to add devices to group")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"group.add_devices", "group", groupID, map[string]int{
				"device_count": len(req.DeviceIDs),
			})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *GroupsHandler) RemoveDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	groupID := chi.URLParam(r, "group_id")
	deviceID := chi.URLParam(r, "device_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "groups", groupID) {
			Error(w, http.StatusNotFound, "group not found")
			return
		}
	}

	if err := h.store.RemoveDeviceFromGroup(r.Context(), tenantID, groupID, deviceID); err != nil {
		Error(w, http.StatusInternalServerError, "failed to remove device from group")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"group.remove_device", "group", groupID, map[string]string{
				"device_id": deviceID,
			})
	}

	w.WriteHeader(http.StatusNoContent)
}
