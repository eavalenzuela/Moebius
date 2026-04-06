package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
// When the server runs behind a reverse proxy (TLS_MODE=passthrough), the
// middleware falls back to reading the client cert from the X-Client-Cert
// header and verifying the chain against caCertPool.
type MTLSMiddleware struct {
	pool       *pgxpool.Pool
	log        *slog.Logger
	caCertPool *x509.CertPool // non-nil enables X-Client-Cert header fallback
}

// NewMTLSMiddleware creates an MTLSMiddleware. If caCertPool is non-nil, the
// middleware will accept client certificates forwarded via the X-Client-Cert
// header (for deployments where a reverse proxy terminates TLS).
func NewMTLSMiddleware(pool *pgxpool.Pool, log *slog.Logger, caCertPool *x509.CertPool) *MTLSMiddleware {
	return &MTLSMiddleware{pool: pool, log: log, caCertPool: caCertPool}
}

// Handler wraps an http.Handler with mTLS verification.
func (m *MTLSMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cert *x509.Certificate

		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			// Direct TLS — chain already verified by Go's TLS layer.
			cert = r.TLS.PeerCertificates[0]
		} else if m.caCertPool != nil {
			// Reverse-proxy mode — try X-Client-Cert header.
			var err error
			cert, err = m.certFromHeader(r)
			if err != nil {
				m.log.Debug("X-Client-Cert header parse failed", slog.String("error", err.Error()))
			}
		}

		if cert == nil {
			m.writeError(w, http.StatusUnauthorized, "client certificate required", "missing_cert")
			return
		}

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
			agentID, hex.EncodeToString(cert.SerialNumber.Bytes()),
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

// certFromHeader parses and chain-verifies a PEM certificate from the
// X-Client-Cert header. The header value may be URL-encoded (common with
// Caddy, Envoy) or raw PEM.
func (m *MTLSMiddleware) certFromHeader(r *http.Request) (*x509.Certificate, error) {
	raw := r.Header.Get(clientCertHeader)
	if raw == "" {
		return nil, nil
	}

	// Try URL-decoding first (proxies often encode PEM newlines).
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		decoded = raw
	}

	block, _ := pem.Decode([]byte(decoded))
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s header", clientCertHeader)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate from header: %w", err)
	}

	// Verify the chain against the CA pool. In direct TLS mode Go's TLS
	// layer does this automatically; here we must do it ourselves.
	opts := x509.VerifyOptions{
		Roots:     m.caCertPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		return nil, fmt.Errorf("certificate chain verification failed: %w", err)
	}

	return cert, nil
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
// certificates against the given CA certificate pool. Uses
// VerifyClientCertIfGiven so that non-agent endpoints (enrollment, API key
// auth) can share the same listener without requiring a client cert — the
// mTLS middleware enforces the cert requirement for agent routes.
func NewAgentTLSConfig(caCertPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  caCertPool,
		MinVersion: tls.VersionTLS12,
	}
}
