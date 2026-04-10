package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/pki"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// Store new certificate and supersede prior un-revoked certs for this
	// device in one transaction. Superseding shrinks the post-compromise
	// window for a stolen agent key from the remaining cert lifetime
	// (up to 90 days) to the duration of this request: the old cert is
	// rejected by mTLS on its next use. Graceful rollover is preserved
	// because the agent receives the new cert in the response before the
	// old one becomes invalid at the TLS layer — the revocation only
	// takes effect on the *next* mTLS handshake.
	now := time.Now().UTC()
	certID := models.NewCertificateID()

	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		h.log.Error("begin renewal tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO agent_certificates (id, device_id, serial_number, fingerprint, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		certID, agentID, serialHex, fingerprint, now, now.Add(defaultCertValidity),
	); err != nil {
		h.log.Error("failed to store renewed certificate", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}

	supersededTag, err := tx.Exec(ctx,
		`UPDATE agent_certificates
		    SET revoked_at = $1, revocation_reason = 'superseded_by_renewal'
		  WHERE device_id = $2
		    AND id <> $3
		    AND revoked_at IS NULL`,
		now, agentID, certID,
	)
	if err != nil {
		h.log.Error("failed to supersede prior certificates", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("commit renewal tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store certificate")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, tenantID, agentID, models.ActorTypeAgent,
			"cert.renew", "agent_certificate", certID, map[string]any{
				"cert_serial":       serialHex,
				"superseded_certs":  supersededTag.RowsAffected(),
				"revocation_reason": "superseded_by_renewal",
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
