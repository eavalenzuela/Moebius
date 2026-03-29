package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/moebius-oss/moebius/server/audit"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/server/store"
	"github.com/moebius-oss/moebius/shared/models"
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
	deviceID := chi.URLParam(r, "device_id")

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
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
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
