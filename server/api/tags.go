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

type TagsHandler struct {
	store *store.Store
	audit *audit.Logger
}

func NewTagsHandler(s *store.Store, auditLog *audit.Logger) *TagsHandler {
	return &TagsHandler{store: s, audit: auditLog}
}

func (h *TagsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	tags, err := h.store.ListTags(ctx, tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list tags")
		return
	}
	if tags == nil {
		tags = []models.Tag{}
	}

	// Scope enforcement: filter to scoped tags if TagIDs is set
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if auth.ScopeHasField(scope, "tags") {
			var filtered []models.Tag
			for _, t := range tags {
				if auth.IDInScopeField(scope, "tags", t.ID) {
					filtered = append(filtered, t)
				}
			}
			tags = filtered
			if tags == nil {
				tags = []models.Tag{}
			}
		}
	}

	JSON(w, http.StatusOK, map[string]any{"data": tags})
}

type createTagRequest struct {
	Name string `json:"name"`
}

func (h *TagsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()

	// Scope enforcement: scoped keys cannot create new tags
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if auth.ScopeHasField(scope, "tags") {
			ErrorWithCode(w, http.StatusForbidden, "scope_violation", "scoped keys cannot create new tags")
			return
		}
	}

	var req createTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	t := &models.Tag{
		ID:       models.NewTagID(),
		TenantID: tenantID,
		Name:     req.Name,
	}
	if err := h.store.CreateTag(r.Context(), t); err != nil {
		Error(w, http.StatusInternalServerError, "failed to create tag")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"tag.create", "tag", t.ID, map[string]string{"name": req.Name})
	}

	JSON(w, http.StatusCreated, t)
}

func (h *TagsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	tagID := chi.URLParam(r, "tag_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "tags", tagID) {
			Error(w, http.StatusNotFound, "tag not found")
			return
		}
	}

	if err := h.store.DeleteTag(r.Context(), tenantID, tagID); err != nil {
		Error(w, http.StatusNotFound, "tag not found")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"tag.delete", "tag", tagID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

type addTagsRequest struct {
	TagIDs []string `json:"tag_ids"`
}

func (h *TagsHandler) AddToDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	deviceID := chi.URLParam(r, "device_id")

	// Scope enforcement: check device is in scope (tags are applied to devices)
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.store.Pool(), tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				Error(w, http.StatusNotFound, "device not found")
				return
			}
		}
	}

	var req addTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.TagIDs) == 0 {
		Error(w, http.StatusBadRequest, "tag_ids is required")
		return
	}

	if err := h.store.AddTagsToDevice(r.Context(), tenantID, deviceID, req.TagIDs); err != nil {
		Error(w, http.StatusInternalServerError, "failed to add tags to device")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"tag.add_to_device", "device", deviceID, map[string]int{
				"tag_count": len(req.TagIDs),
			})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TagsHandler) RemoveFromDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	deviceID := chi.URLParam(r, "device_id")
	tagID := chi.URLParam(r, "tag_id")

	// Scope enforcement: check device is in scope
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.store.Pool(), tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				Error(w, http.StatusNotFound, "device not found")
				return
			}
		}
	}

	if err := h.store.RemoveTagFromDevice(r.Context(), tenantID, deviceID, tagID); err != nil {
		Error(w, http.StatusInternalServerError, "failed to remove tag from device")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"tag.remove_from_device", "device", deviceID, map[string]string{
				"tag_id": tagID,
			})
	}

	w.WriteHeader(http.StatusNoContent)
}
