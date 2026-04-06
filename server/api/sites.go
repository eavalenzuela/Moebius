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

type SitesHandler struct {
	store *store.Store
	audit *audit.Logger
}

func NewSitesHandler(s *store.Store, auditLog *audit.Logger) *SitesHandler {
	return &SitesHandler{store: s, audit: auditLog}
}

func (h *SitesHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	sites, err := h.store.ListSites(ctx, tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list sites")
		return
	}
	if sites == nil {
		sites = []models.Site{}
	}

	// Scope enforcement: filter to scoped sites if SiteIDs is set
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if auth.ScopeHasField(scope, "sites") {
			var filtered []models.Site
			for _, s := range sites {
				if auth.IDInScopeField(scope, "sites", s.ID) {
					filtered = append(filtered, s)
				}
			}
			sites = filtered
			if sites == nil {
				sites = []models.Site{}
			}
		}
	}

	JSON(w, http.StatusOK, map[string]any{"data": sites})
}

func (h *SitesHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	siteID := chi.URLParam(r, "site_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "sites", siteID) {
			Error(w, http.StatusNotFound, "site not found")
			return
		}
	}

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
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()

	// Scope enforcement: scoped keys cannot create new sites (they can only see scoped sites)
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if auth.ScopeHasField(scope, "sites") {
			ErrorWithCode(w, http.StatusForbidden, "scope_violation", "scoped keys cannot create new sites")
			return
		}
	}

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

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"site.create", "site", si.ID, map[string]string{"name": req.Name})
	}

	JSON(w, http.StatusCreated, si)
}

func (h *SitesHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	siteID := chi.URLParam(r, "site_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "sites", siteID) {
			Error(w, http.StatusNotFound, "site not found")
			return
		}
	}

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

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"site.update", "site", siteID, nil)
	}

	site, _ := h.store.GetSite(r.Context(), tenantID, siteID)
	JSON(w, http.StatusOK, site)
}

func (h *SitesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	siteID := chi.URLParam(r, "site_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "sites", siteID) {
			Error(w, http.StatusNotFound, "site not found")
			return
		}
	}

	if err := h.store.DeleteSite(r.Context(), tenantID, siteID); err != nil {
		Error(w, http.StatusNotFound, "site not found")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"site.delete", "site", siteID, nil)
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
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	siteID := chi.URLParam(r, "site_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "sites", siteID) {
			Error(w, http.StatusNotFound, "site not found")
			return
		}
	}

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

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"site.add_devices", "site", siteID, map[string]int{
				"device_count": len(req.DeviceIDs),
			})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *SitesHandler) RemoveDevice(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	siteID := chi.URLParam(r, "site_id")
	deviceID := chi.URLParam(r, "device_id")

	if !auth.IsAdminFromContext(ctx) {
		if !auth.IDInScopeField(auth.ScopeFromContext(ctx), "sites", siteID) {
			Error(w, http.StatusNotFound, "site not found")
			return
		}
	}

	if err := h.store.RemoveDeviceFromSite(r.Context(), tenantID, siteID, deviceID); err != nil {
		Error(w, http.StatusInternalServerError, "failed to remove device from site")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"site.remove_device", "site", siteID, map[string]string{
				"device_id": deviceID,
			})
	}

	w.WriteHeader(http.StatusNoContent)
}
