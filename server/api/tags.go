package api

import (
	"encoding/json"
	"net/http"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
)

type TagsHandler struct {
	store *store.Store
}

func NewTagsHandler(s *store.Store) *TagsHandler {
	return &TagsHandler{store: s}
}

func (h *TagsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	tags, err := h.store.ListTags(r.Context(), tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list tags")
		return
	}
	if tags == nil {
		tags = []models.Tag{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": tags})
}

type createTagRequest struct {
	Name string `json:"name"`
}

func (h *TagsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

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
	JSON(w, http.StatusCreated, t)
}

func (h *TagsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	tagID := chi.URLParam(r, "tag_id")

	if err := h.store.DeleteTag(r.Context(), tenantID, tagID); err != nil {
		Error(w, http.StatusNotFound, "tag not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type addTagsRequest struct {
	TagIDs []string `json:"tag_ids"`
}

func (h *TagsHandler) AddToDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	deviceID := chi.URLParam(r, "device_id")

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
	w.WriteHeader(http.StatusNoContent)
}

func (h *TagsHandler) RemoveFromDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	deviceID := chi.URLParam(r, "device_id")
	tagID := chi.URLParam(r, "tag_id")

	if err := h.store.RemoveTagFromDevice(r.Context(), tenantID, deviceID, tagID); err != nil {
		Error(w, http.StatusInternalServerError, "failed to remove tag from device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
