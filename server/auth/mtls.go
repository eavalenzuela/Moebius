package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	// ContextKeyAgentID is the context key for the authenticated agent's device ID.
	ContextKeyAgentID contextKey = "agent_id"
	// ContextKeyTenantID is the context key for the authenticated agent's tenant ID.
	ContextKeyTenantID contextKey = "tenant_id"
)

// AgentIDFromContext extracts the agent ID from the request context.
func AgentIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyAgentID).(string)
	return v
}

// TenantIDFromContext extracts the tenant ID from the request context.
func TenantIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyTenantID).(string)
	return v
}

// MTLSMiddleware verifies agent client certificates and attaches identity to context.
type MTLSMiddleware struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewMTLSMiddleware creates an MTLSMiddleware.
func NewMTLSMiddleware(pool *pgxpool.Pool, log *slog.Logger) *MTLSMiddleware {
	return &MTLSMiddleware{pool: pool, log: log}
}

// Handler wraps an http.Handler with mTLS verification.
func (m *MTLSMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			m.writeError(w, http.StatusUnauthorized, "client certificate required", "missing_cert")
			return
		}

		cert := r.TLS.PeerCertificates[0]

		// Extract agent_id from the certificate CN (also in DNS SAN).
		agentID := cert.Subject.CommonName
		if agentID == "" {
			m.writeError(w, http.StatusUnauthorized, "certificate missing agent identity", "no_agent_id")
			return
		}

		// Check expiry
		now := time.Now()
		if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			m.log.Warn("agent cert expired", slog.String("agent_id", agentID))
			m.writeError(w, http.StatusUnauthorized, "certificate expired", "cert_expired")
			return
		}

		// Check revocation and resolve tenant from DB
		var tenantID string
		var deviceStatus string
		var revokedAt *time.Time

		// Find the device and check cert revocation in one query
		err := m.pool.QueryRow(r.Context(),
			`SELECT d.tenant_id, d.status, ac.revoked_at
			 FROM devices d
			 JOIN agent_certificates ac ON ac.device_id = d.id
			 WHERE d.id = $1 AND ac.serial_number = $2`,
			agentID, cert.SerialNumber.Text(16),
		).Scan(&tenantID, &deviceStatus, &revokedAt)

		if err != nil {
			if err == pgx.ErrNoRows {
				m.log.Warn("unknown agent or cert", slog.String("agent_id", agentID))
				m.writeError(w, http.StatusUnauthorized, "unknown device or certificate", "unknown_device")
				return
			}
			m.log.Error("mtls db lookup failed", slog.String("error", err.Error()))
			m.writeError(w, http.StatusInternalServerError, "internal error", "")
			return
		}

		if revokedAt != nil {
			m.log.Warn("agent cert revoked", slog.String("agent_id", agentID))
			m.writeError(w, http.StatusUnauthorized, "certificate revoked", "cert_revoked")
			return
		}

		if deviceStatus == "revoked" {
			m.log.Warn("device revoked", slog.String("agent_id", agentID))
			m.writeError(w, http.StatusUnauthorized, "device revoked", "device_revoked")
			return
		}

		// Attach identity to context
		ctx := context.WithValue(r.Context(), ContextKeyAgentID, agentID)
		ctx = context.WithValue(ctx, ContextKeyTenantID, tenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *MTLSMiddleware) writeError(w http.ResponseWriter, status int, msg, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := `{"error":"` + msg + `"`
	if reason != "" {
		body += `,"reason":"` + reason + `"`
	}
	body += `}`
	_, _ = w.Write([]byte(body))
}

// NewAgentTLSConfig creates a tls.Config that requests and verifies client
// certificates against the given CA certificate pool.
func NewAgentTLSConfig(caCertPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		ClientAuth: tls.RequireAnyClientCert,
		ClientCAs:  caCertPool,
		MinVersion: tls.VersionTLS12,
	}
}
