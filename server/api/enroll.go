package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/pki"
	"github.com/eavalenzuela/Moebius/server/quota"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultCertValidity = 90 * 24 * time.Hour

// EnrollHandler handles POST /v1/agents/enroll.
type EnrollHandler struct {
	pool       *pgxpool.Pool
	enrollment *auth.EnrollmentService
	ca         *pki.CA
	audit      *audit.Logger
	quota      *quota.Resolver
	log        *slog.Logger
}

// NewEnrollHandler creates an EnrollHandler.
func NewEnrollHandler(pool *pgxpool.Pool, enrollment *auth.EnrollmentService, ca *pki.CA, auditLog *audit.Logger, quotaRes *quota.Resolver, log *slog.Logger) *EnrollHandler {
	return &EnrollHandler{
		pool:       pool,
		enrollment: enrollment,
		ca:         ca,
		audit:      auditLog,
		quota:      quotaRes,
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

	// Device count quota — rejected after token consume so a rate-of-
	// enrollment attack cannot skip the consume and replay the same
	// token. The quota is best-effort: concurrent enrolls against an
	// almost-full tenant can both pass, but at most by a few devices.
	if err := h.quota.CheckDevices(ctx, token.TenantID); err != nil {
		if HandleQuotaError(w, err) {
			return
		}
		h.log.Error("device quota check", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to check quota")
		return
	}

	// 2. Sign the CSR (no DB writes — safe to do before opening the tx)
	deviceID := models.NewDeviceID()
	now := time.Now().UTC()

	certPEM, serialHex, fingerprint, err := h.ca.SignCSR([]byte(req.CSR), deviceID, defaultCertValidity)
	if err != nil {
		h.log.Error("failed to sign CSR", slog.String("error", err.Error()))
		Error(w, http.StatusBadRequest, "invalid CSR")
		return
	}

	// 3. Atomically create device + apply scope + store cert. If any step
	// fails the whole enrollment rolls back so we never leave a half-scoped
	// device or a device without a usable cert.
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		h.log.Error("begin tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to enroll")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO devices (id, tenant_id, hostname, os, os_version, arch, agent_version, status, registered_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		deviceID, token.TenantID, req.Hostname, req.OS, req.OSVersion, req.Arch, req.AgentVersion,
		models.DeviceStatusOnline, now,
	); err != nil {
		h.log.Error("failed to create device", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create device")
		return
	}

	if token.Scope != nil {
		if err := h.applyScope(ctx, tx, deviceID, token.TenantID, token.Scope); err != nil {
			h.log.Warn("token scope application failed",
				slog.String("device_id", deviceID),
				slog.String("error", err.Error()))
			ErrorWithCode(w, http.StatusBadRequest, "invalid_scope",
				"token scope references unknown or cross-tenant resources")
			return
		}
	}

	certID := models.NewCertificateID()
	if _, err := tx.Exec(ctx,
		`INSERT INTO agent_certificates (id, device_id, serial_number, fingerprint, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		certID, deviceID, serialHex, fingerprint, now, now.Add(defaultCertValidity),
	); err != nil {
		h.log.Error("failed to store certificate", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("commit enrollment tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to enroll")
		return
	}

	// 6. Audit log
	if h.audit != nil {
		h.audit.LogAction(ctx, token.TenantID, deviceID, models.ActorTypeAgent,
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

// applyScope inserts the token's group/tag/site memberships for the new
// device, refusing any reference whose tenant does not match the device.
// Defense-in-depth against cross-tenant pollution: token creation already
// validates this, but if a referenced row was deleted between token creation
// and enrollment, or a different operator/path produced the token, this is
// the last line of defense before we materialize the membership.
func (h *EnrollHandler) applyScope(ctx context.Context, tx pgx.Tx, deviceID, tenantID string, scope *models.APIScope) error {
	insert := func(membershipTable, parentTable, idCol, id string) error {
		// Table/column names are hardcoded constants from the call sites
		// below — never user input — so the fmt.Sprintf is safe.
		query := fmt.Sprintf(
			`INSERT INTO %s (device_id, %s)
			 SELECT $1, $2 WHERE EXISTS (SELECT 1 FROM %s WHERE id = $2 AND tenant_id = $3)
			 ON CONFLICT DO NOTHING`,
			membershipTable, idCol, parentTable,
		)
		tag, err := tx.Exec(ctx, query, deviceID, id, tenantID)
		if err != nil {
			return fmt.Errorf("apply %s %s: %w", parentTable, id, err)
		}
		if tag.RowsAffected() == 0 {
			// Either the parent row is missing/cross-tenant, OR the
			// membership row already exists. Distinguish by checking
			// the parent — only the cross-tenant case is fatal.
			existsQuery := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND tenant_id = $2)`, parentTable)
			var exists bool
			if err := tx.QueryRow(ctx, existsQuery, id, tenantID).Scan(&exists); err != nil {
				return fmt.Errorf("verify %s %s: %w", parentTable, id, err)
			}
			if !exists {
				return fmt.Errorf("%s %s not found in tenant", parentTable, id)
			}
		}
		return nil
	}

	for _, groupID := range scope.GroupIDs {
		if err := insert("device_groups", "groups", "group_id", groupID); err != nil {
			return err
		}
	}
	for _, tagID := range scope.TagIDs {
		if err := insert("device_tags", "tags", "tag_id", tagID); err != nil {
			return err
		}
	}
	for _, siteID := range scope.SiteIDs {
		if err := insert("device_sites", "sites", "site_id", siteID); err != nil {
			return err
		}
	}
	return nil
}
