package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moebius-oss/moebius/server/audit"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/server/pki"
	"github.com/moebius-oss/moebius/shared/models"
	"github.com/moebius-oss/moebius/shared/protocol"
)

const defaultCertValidity = 90 * 24 * time.Hour

// EnrollHandler handles POST /v1/agents/enroll.
type EnrollHandler struct {
	pool       *pgxpool.Pool
	enrollment *auth.EnrollmentService
	ca         *pki.CA
	audit      *audit.Logger
	log        *slog.Logger
}

// NewEnrollHandler creates an EnrollHandler.
func NewEnrollHandler(pool *pgxpool.Pool, enrollment *auth.EnrollmentService, ca *pki.CA, auditLog *audit.Logger, log *slog.Logger) *EnrollHandler {
	return &EnrollHandler{
		pool:       pool,
		enrollment: enrollment,
		ca:         ca,
		audit:      auditLog,
		log:        log,
	}
}

// ServeHTTP handles agent enrollment requests.
func (h *EnrollHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		Error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req protocol.EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.EnrollmentToken == "" || req.CSR == "" || req.Hostname == "" {
		Error(w, http.StatusBadRequest, "enrollment_token, csr, and hostname are required")
		return
	}

	ctx := r.Context()

	// 1. Validate and consume enrollment token
	token, err := h.enrollment.ValidateAndConsume(ctx, req.EnrollmentToken)
	if err != nil {
		h.log.Warn("enrollment token validation failed", slog.String("error", err.Error()))
		Error(w, http.StatusUnauthorized, "invalid or expired enrollment token")
		return
	}

	// 2. Create device record
	deviceID := models.NewDeviceID()
	now := time.Now().UTC()

	_, err = h.pool.Exec(ctx,
		`INSERT INTO devices (id, tenant_id, hostname, os, os_version, arch, agent_version, status, registered_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		deviceID, token.TenantID, req.Hostname, req.OS, req.OSVersion, req.Arch, req.AgentVersion,
		models.DeviceStatusOnline, now,
	)
	if err != nil {
		h.log.Error("failed to create device", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create device")
		return
	}

	// 3. Inherit scope from token (groups, tags, sites)
	if token.Scope != nil {
		if err := h.applyScope(ctx, deviceID, token.Scope); err != nil {
			h.log.Error("failed to apply token scope", slog.String("error", err.Error()))
			// Device is created but scope failed — non-fatal, continue
		}
	}

	// 4. Sign the CSR
	certPEM, serialHex, fingerprint, err := h.ca.SignCSR([]byte(req.CSR), deviceID, defaultCertValidity)
	if err != nil {
		h.log.Error("failed to sign CSR", slog.String("error", err.Error()))
		Error(w, http.StatusBadRequest, "invalid CSR")
		return
	}

	// 5. Store certificate record
	certID := models.NewCertificateID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO agent_certificates (id, device_id, serial_number, fingerprint, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		certID, deviceID, serialHex, fingerprint, now, now.Add(defaultCertValidity),
	)
	if err != nil {
		h.log.Error("failed to store certificate", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}

	// 6. Audit log
	if h.audit != nil {
		_ = h.audit.LogAction(ctx, token.TenantID, deviceID, models.ActorTypeAgent,
			"device.enroll", "device", deviceID, map[string]string{
				"hostname":    req.Hostname,
				"token_id":    token.ID,
				"cert_serial": serialHex,
			})
	}

	// 7. Build CA chain
	caChain := string(h.ca.CAChainPEM())

	resp := protocol.EnrollResponse{
		AgentID:             deviceID,
		Certificate:         string(certPEM),
		CAChain:             caChain,
		PollIntervalSeconds: 30,
	}

	h.log.Info("agent enrolled",
		slog.String("device_id", deviceID),
		slog.String("hostname", req.Hostname),
		slog.String("tenant_id", token.TenantID),
	)

	JSON(w, http.StatusOK, resp)
}

func (h *EnrollHandler) applyScope(ctx context.Context, deviceID string, scope *models.APIScope) error {
	for _, groupID := range scope.GroupIDs {
		_, err := h.pool.Exec(ctx,
			`INSERT INTO device_groups (device_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			deviceID, groupID,
		)
		if err != nil {
			return err
		}
	}
	for _, tagID := range scope.TagIDs {
		_, err := h.pool.Exec(ctx,
			`INSERT INTO device_tags (device_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			deviceID, tagID,
		)
		if err != nil {
			return err
		}
	}
	for _, siteID := range scope.SiteIDs {
		_, err := h.pool.Exec(ctx,
			`INSERT INTO device_sites (device_id, site_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			deviceID, siteID,
		)
		if err != nil {
			return err
		}
	}
	return nil
}
