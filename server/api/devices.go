package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
)

// DevicesHandler handles /v1/devices endpoints.
type DevicesHandler struct {
	store *store.Store
	audit *audit.Logger
	log   *slog.Logger
}

// NewDevicesHandler creates a DevicesHandler.
func NewDevicesHandler(s *store.Store, auditLog *audit.Logger, log *slog.Logger) *DevicesHandler {
	return &DevicesHandler{store: s, audit: auditLog, log: log}
}

type revokeDeviceRequest struct {
	Reason string `json:"reason,omitempty"`
}

// Revoke handles POST /v1/devices/{device_id}/revoke.
func (h *DevicesHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ctx := r.Context()
	deviceID := chi.URLParam(r, "device_id")

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.store.Pool(), tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				ErrorWithCode(w, http.StatusNotFound, "device_not_found", "Device not found or already revoked")
				return
			}
		}
	}

	var req revokeDeviceRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if req.Reason == "" {
		req.Reason = "operator_revoked"
	}

	if err := h.store.RevokeDevice(r.Context(), tenantID, deviceID, req.Reason); err != nil {
		h.log.Error("failed to revoke device", slog.String("error", err.Error()))
		ErrorWithCode(w, http.StatusNotFound, "device_not_found", "Device not found or already revoked")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"device.revoke", "device", deviceID, map[string]string{
				"reason": req.Reason,
			})
	}

	h.log.Info("device revoked",
		slog.String("device_id", deviceID),
		slog.String("tenant_id", tenantID),
		slog.String("by", userID),
	)

	w.WriteHeader(http.StatusNoContent)
}

// List handles GET /v1/devices.
func (h *DevicesHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	q := r.URL.Query()

	// Scope enforcement — restrict device list to allowed device IDs
	var scopeDeviceIDs []string
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if scope != nil {
			allowed, err := auth.ResolveScope(ctx, h.store.Pool(), tenantID, scope)
			if err != nil {
				h.log.Error("failed to resolve scope", slog.String("error", err.Error()))
				Error(w, http.StatusInternalServerError, "failed to resolve scope")
				return
			}
			for id := range allowed {
				scopeDeviceIDs = append(scopeDeviceIDs, id)
			}
			if len(scopeDeviceIDs) == 0 {
				JSON(w, http.StatusOK, map[string]any{"data": []models.Device{}})
				return
			}
		}
	}

	devices, err := h.store.ListDevices(ctx, tenantID, store.DeviceFilters{
		Status:         q.Get("status"),
		GroupID:        q.Get("group_id"),
		TagID:          q.Get("tag_id"),
		SiteID:         q.Get("site_id"),
		OS:             q.Get("os"),
		Search:         q.Get("search"),
		ScopeDeviceIDs: scopeDeviceIDs,
	})
	if err != nil {
		h.log.Error("failed to list devices", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list devices")
		return
	}
	if devices == nil {
		devices = []models.Device{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": devices})
}

// Get handles GET /v1/devices/{device_id}.
func (h *DevicesHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	deviceID := chi.URLParam(r, "device_id")

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.store.Pool(), tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				Error(w, http.StatusNotFound, "device not found") // 404, not 403 — don't leak existence
				return
			}
		}
	}

	device, err := h.store.GetDevice(ctx, tenantID, deviceID)
	if err != nil {
		h.log.Error("failed to get device", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get device")
		return
	}
	if device == nil {
		Error(w, http.StatusNotFound, "device not found")
		return
	}
	JSON(w, http.StatusOK, device)
}

type updateDeviceRequest struct {
	Hostname string `json:"hostname"`
}

// Update handles PATCH /v1/devices/{device_id}.
func (h *DevicesHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()
	deviceID := chi.URLParam(r, "device_id")

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.store.Pool(), tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				Error(w, http.StatusNotFound, "device not found")
				return
			}
		}
	}

	var req updateDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Hostname == "" {
		Error(w, http.StatusBadRequest, "hostname is required")
		return
	}

	if err := h.store.UpdateDevice(r.Context(), tenantID, deviceID, req.Hostname); err != nil {
		Error(w, http.StatusNotFound, "device not found")
		return
	}

	device, err := h.store.GetDevice(r.Context(), tenantID, deviceID)
	if err != nil || device == nil {
		Error(w, http.StatusInternalServerError, "failed to get updated device")
		return
	}
	JSON(w, http.StatusOK, device)
}
