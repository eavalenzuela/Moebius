package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OIDCConfig holds OIDC provider settings.
type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
}

// OIDCMiddleware authenticates requests via OIDC Bearer tokens.
// It maps the OIDC subject to a user record via users.sso_subject.
type OIDCMiddleware struct {
	pool     *pgxpool.Pool
	verifier *oidc.IDTokenVerifier
	log      *slog.Logger
}

// NewOIDCMiddleware creates an OIDCMiddleware. Returns nil if OIDC is not configured.
func NewOIDCMiddleware(ctx context.Context, pool *pgxpool.Pool, cfg OIDCConfig, log *slog.Logger) (*OIDCMiddleware, error) {
	if cfg.IssuerURL == "" || cfg.ClientID == "" {
		return nil, nil //nolint:nilnil // nil means OIDC not configured
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("create OIDC provider: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	return &OIDCMiddleware{
		pool:     pool,
		verifier: verifier,
		log:      log,
	}, nil
}

// Handler wraps an http.Handler with OIDC authentication.
// It only activates if the Authorization header contains a Bearer token
// that is NOT an API key (sk_ prefix). This allows both auth methods
// to coexist on the same routes.
func (m *OIDCMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" || strings.HasPrefix(token, "sk_") {
			// Not an OIDC token — pass through to next middleware
			next.ServeHTTP(w, r)
			return
		}

		// Verify the OIDC token
		idToken, err := m.verifier.Verify(r.Context(), token)
		if err != nil {
			m.log.Warn("OIDC token verification failed", slog.String("error", err.Error()))
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"code":    "invalid_token",
					"message": "The provided OIDC token is not valid",
				},
			})
			return
		}

		// Extract claims
		var claims struct {
			Subject string `json:"sub"`
			Email   string `json:"email"`
		}
		if err := idToken.Claims(&claims); err != nil {
			m.log.Error("failed to extract OIDC claims", slog.String("error", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{
					"code":    "internal_error",
					"message": "Failed to process token claims",
				},
			})
			return
		}

		// Look up user by SSO subject
		var userID, tenantID, roleID string
		var permissions []byte

		err = m.pool.QueryRow(r.Context(),
			`SELECT u.id, u.tenant_id, u.role_id, r.permissions
			 FROM users u
			 LEFT JOIN roles r ON r.id = u.role_id
			 WHERE u.sso_subject = $1`,
			claims.Subject,
		).Scan(&userID, &tenantID, &roleID, &permissions)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": map[string]string{
						"code":    "unknown_sso_user",
						"message": "No user mapped to this SSO identity",
					},
				})
				return
			}
			m.log.Error("SSO user lookup failed", slog.String("error", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{
					"code":    "internal_error",
					"message": "Internal server error",
				},
			})
			return
		}

		var perms []string
		if permissions != nil {
			_ = json.Unmarshal(permissions, &perms)
		}

		ctx := context.WithValue(r.Context(), ContextKeyTenantID, tenantID)
		ctx = context.WithValue(ctx, ContextKeyUserID, userID)
		ctx = context.WithValue(ctx, ContextKeyRoleID, roleID)
		ctx = context.WithValue(ctx, ContextKeyPermissions, perms)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
