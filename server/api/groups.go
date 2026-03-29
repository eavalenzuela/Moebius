package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/server/store"
	"github.com/moebius-oss/moebius/shared/models"
)

// GroupsHandler handles /v1/groups endpoints.
type GroupsHandler struct {
	store *store.Store
}

// NewGroupsHandler creates a GroupsHandler.
func NewGroupsHandler(s *store.Store) *GroupsHandler {
	return &GroupsHandler{store: s}
}

func (h *GroupsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	groups, err := h.store.ListGroups(r.Context(), tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	if groups == nil {
		groups = []models.Group{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": groups})
}

func (h *GroupsHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	groupID := chi.URLParam(r, "group_id")

	group, err := h.store.GetGroup(r.Context(), tenantID, groupID)
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
	JSON(w, http.StatusCreated, g)
}

func (h *GroupsHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	groupID := chi.URLParam(r, "group_id")

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

	group, _ := h.store.GetGroup(r.Context(), tenantID, groupID)
	JSON(w, http.StatusOK, group)
}

func (h *GroupsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	groupID := chi.URLParam(r, "group_id")

	if err := h.store.DeleteGroup(r.Context(), tenantID, groupID); err != nil {
		Error(w, http.StatusNotFound, "group not found")
		return
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
	groupID := chi.URLParam(r, "group_id")

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
	w.WriteHeader(http.StatusNoContent)
}

func (h *GroupsHandler) RemoveDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	groupID := chi.URLParam(r, "group_id")
	deviceID := chi.URLParam(r, "device_id")

	if err := h.store.RemoveDeviceFromGroup(r.Context(), tenantID, groupID, deviceID); err != nil {
		Error(w, http.StatusInternalServerError, "failed to remove device from group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
