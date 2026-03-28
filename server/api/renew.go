package api

import (
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

// RenewHandler handles POST /v1/agents/renew (mTLS-authenticated).
type RenewHandler struct {
	pool  *pgxpool.Pool
	ca    *pki.CA
	audit *audit.Logger
	log   *slog.Logger
}

// NewRenewHandler creates a RenewHandler.
func NewRenewHandler(pool *pgxpool.Pool, ca *pki.CA, auditLog *audit.Logger, log *slog.Logger) *RenewHandler {
	return &RenewHandler{pool: pool, ca: ca, audit: auditLog, log: log}
}

// ServeHTTP handles certificate renewal requests.
func (h *RenewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		Error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	agentID := auth.AgentIDFromContext(ctx)
	tenantID := auth.TenantIDFromContext(ctx)

	if agentID == "" {
		Error(w, http.StatusUnauthorized, "agent identity required")
		return
	}

	var req protocol.RenewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.CSR == "" {
		Error(w, http.StatusBadRequest, "csr is required")
		return
	}

	// Sign the new CSR
	certPEM, serialHex, fingerprint, err := h.ca.SignCSR([]byte(req.CSR), agentID, defaultCertValidity)
	if err != nil {
		h.log.Error("failed to sign renewal CSR", slog.String("error", err.Error()), slog.String("agent_id", agentID))
		Error(w, http.StatusBadRequest, "invalid CSR")
		return
	}

	// Store new certificate record (old cert remains valid until expiry)
	now := time.Now().UTC()
	certID := models.NewCertificateID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO agent_certificates (id, device_id, serial_number, fingerprint, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		certID, agentID, serialHex, fingerprint, now, now.Add(defaultCertValidity),
	)
	if err != nil {
		h.log.Error("failed to store renewed certificate", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}

	// Audit log
	if h.audit != nil {
		_ = h.audit.LogAction(ctx, tenantID, agentID, models.ActorTypeAgent,
			"cert.renew", "agent_certificate", certID, map[string]string{
				"cert_serial": serialHex,
			})
	}

	h.log.Info("agent certificate renewed",
		slog.String("agent_id", agentID),
		slog.String("tenant_id", tenantID),
	)

	resp := protocol.RenewResponse{
		Certificate: string(certPEM),
		CAChain:     string(h.ca.CAChainPEM()),
	}

	JSON(w, http.StatusOK, resp)
}
