package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moebius-oss/moebius/shared/models"
)

const (
	// ContextKeyUserID is the context key for the authenticated user's ID.
	ContextKeyUserID contextKey = "user_id"
	// ContextKeyRoleID is the context key for the authenticated role ID.
	ContextKeyRoleID contextKey = "role_id"
	// ContextKeyPermissions is the context key for the role's permissions.
	ContextKeyPermissions contextKey = "permissions"
	// ContextKeyScope is the context key for the API key's scope.
	ContextKeyScope contextKey = "scope"
	// ContextKeyIsAdmin is the context key for admin flag.
	ContextKeyIsAdmin contextKey = "is_admin"
)

// UserIDFromContext extracts the user ID from the request context.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyUserID).(string)
	return v
}

// PermissionsFromContext extracts the permissions from the request context.
func PermissionsFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(ContextKeyPermissions).([]string)
	return v
}

// ScopeFromContext extracts the API key scope from the request context.
func ScopeFromContext(ctx context.Context) *models.APIScope {
	v, _ := ctx.Value(ContextKeyScope).(*models.APIScope)
	return v
}

// IsAdminFromContext returns whether the authenticated identity is an admin.
func IsAdminFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(ContextKeyIsAdmin).(bool)
	return v
}

// APIKeyMiddleware authenticates requests via Bearer token.
type APIKeyMiddleware struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewAPIKeyMiddleware creates an APIKeyMiddleware.
func NewAPIKeyMiddleware(pool *pgxpool.Pool, log *slog.Logger) *APIKeyMiddleware {
	return &APIKeyMiddleware{pool: pool, log: log}
}

// Handler wraps an http.Handler with API key authentication.
func (m *APIKeyMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := extractBearerToken(r)
		if raw == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"code":    "authentication_required",
					"message": "Authorization: Bearer <api_key> header is required",
				},
			})
			return
		}

		keyHash := hashAPIKey(raw)
		ctx := r.Context()

		var key models.APIKey
		var scopeJSON []byte
		var permissions []string

		err := m.pool.QueryRow(ctx,
			`SELECT k.id, k.tenant_id, k.user_id, k.name, k.role_id, k.scope, k.is_admin, k.expires_at,
			        r.permissions
			 FROM api_keys k
			 LEFT JOIN roles r ON r.id = k.role_id
			 WHERE k.key_hash = $1`,
			keyHash,
		).Scan(
			&key.ID, &key.TenantID, &key.UserID, &key.Name, &key.RoleID,
			&scopeJSON, &key.IsAdmin, &key.ExpiresAt,
			&permissions,
		)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": map[string]string{
						"code":    "invalid_api_key",
						"message": "The provided API key is not valid",
					},
				})
				return
			}
			m.log.Error("api key lookup failed", slog.String("error", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]string{
					"code":    "internal_error",
					"message": "Internal server error",
				},
			})
			return
		}

		// Check expiry
		if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"code":    "api_key_expired",
					"message": "The API key has expired",
				},
			})
			return
		}

		// Parse scope
		var scope *models.APIScope
		if scopeJSON != nil {
			scope = &models.APIScope{}
			if err := json.Unmarshal(scopeJSON, scope); err != nil {
				m.log.Error("failed to parse api key scope", slog.String("error", err.Error()))
			}
		}

		// Update last_used_at (fire-and-forget — uses background context
		// intentionally since the request context will be cancelled)
		go func() { //nolint:gosec // intentional: outlives request context
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = m.pool.Exec(bgCtx,
				`UPDATE api_keys SET last_used_at = $1 WHERE id = $2`,
				time.Now().UTC(), key.ID,
			)
		}()

		// Attach identity to context
		ctx = context.WithValue(ctx, ContextKeyTenantID, key.TenantID)
		ctx = context.WithValue(ctx, ContextKeyUserID, key.UserID)
		ctx = context.WithValue(ctx, ContextKeyRoleID, key.RoleID)
		ctx = context.WithValue(ctx, ContextKeyPermissions, permissions)
		ctx = context.WithValue(ctx, ContextKeyScope, scope)
		ctx = context.WithValue(ctx, ContextKeyIsAdmin, key.IsAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func hashAPIKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
