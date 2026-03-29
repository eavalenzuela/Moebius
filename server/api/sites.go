package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/server/store"
	"github.com/moebius-oss/moebius/shared/models"
)

type SitesHandler struct {
	store *store.Store
}

func NewSitesHandler(s *store.Store) *SitesHandler {
	return &SitesHandler{store: s}
}

func (h *SitesHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	sites, err := h.store.ListSites(r.Context(), tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list sites")
		return
	}
	if sites == nil {
		sites = []models.Site{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": sites})
}

func (h *SitesHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	siteID := chi.URLParam(r, "site_id")

	site, err := h.store.GetSite(r.Context(), tenantID, siteID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get site")
		return
	}
	if site == nil {
		Error(w, http.StatusNotFound, "site not found")
		return
	}
	JSON(w, http.StatusOK, site)
}

type createSiteRequest struct {
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
}

func (h *SitesHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	var req createSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	si := &models.Site{
		ID:       models.NewSiteID(),
		TenantID: tenantID,
		Name:     req.Name,
		Location: req.Location,
	}
	if err := h.store.CreateSite(r.Context(), si); err != nil {
		Error(w, http.StatusInternalServerError, "failed to create site")
		return
	}
	JSON(w, http.StatusCreated, si)
}

func (h *SitesHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	siteID := chi.URLParam(r, "site_id")

	var req createSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := h.store.UpdateSite(r.Context(), tenantID, siteID, req.Name, req.Location); err != nil {
		Error(w, http.StatusNotFound, "site not found")
		return
	}

	site, _ := h.store.GetSite(r.Context(), tenantID, siteID)
	JSON(w, http.StatusOK, site)
}

func (h *SitesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	siteID := chi.URLParam(r, "site_id")

	if err := h.store.DeleteSite(r.Context(), tenantID, siteID); err != nil {
		Error(w, http.StatusNotFound, "site not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SitesHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	siteID := chi.URLParam(r, "site_id")

	devices, err := h.store.ListSiteDevices(r.Context(), tenantID, siteID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list site devices")
		return
	}
	if devices == nil {
		devices = []models.Device{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": devices})
}

type addSiteDevicesRequest struct {
	DeviceIDs []string `json:"device_ids"`
}

func (h *SitesHandler) AddDevices(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	siteID := chi.URLParam(r, "site_id")

	var req addSiteDevicesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.DeviceIDs) == 0 {
		Error(w, http.StatusBadRequest, "device_ids is required")
		return
	}

	if err := h.store.AddDevicesToSite(r.Context(), tenantID, siteID, req.DeviceIDs); err != nil {
		Error(w, http.StatusInternalServerError, "failed to add devices to site")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SitesHandler) RemoveDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	siteID := chi.URLParam(r, "site_id")
	deviceID := chi.URLParam(r, "device_id")

	if err := h.store.RemoveDeviceFromSite(r.Context(), tenantID, siteID, deviceID); err != nil {
		Error(w, http.StatusInternalServerError, "failed to remove device from site")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
