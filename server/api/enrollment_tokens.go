package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnrollmentTokensHandler handles /v1/enrollment-tokens endpoints.
type EnrollmentTokensHandler struct {
	pool       *pgxpool.Pool
	enrollment *auth.EnrollmentService
	audit      *audit.Logger
	log        *slog.Logger
}

// NewEnrollmentTokensHandler creates an EnrollmentTokensHandler.
func NewEnrollmentTokensHandler(pool *pgxpool.Pool, enrollment *auth.EnrollmentService, auditLog *audit.Logger, log *slog.Logger) *EnrollmentTokensHandler {
	return &EnrollmentTokensHandler{
		pool:       pool,
		enrollment: enrollment,
		audit:      auditLog,
		log:        log,
	}
}

type createTokenRequest struct {
	ExpiresInSeconds int              `json:"expires_in_seconds,omitempty"`
	Scope            *models.APIScope `json:"scope,omitempty"`
}

type createTokenResponse struct {
	ID        string           `json:"id"`
	Token     string           `json:"token"`
	ExpiresAt time.Time        `json:"expires_at"`
	Scope     *models.APIScope `json:"scope,omitempty"`
}

// Create handles POST /v1/enrollment-tokens.
func (h *EnrollmentTokensHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req createTokenRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			Error(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	// Scope enforcement: token scope must be a subset of the API key's scope
	if !auth.IsAdminFromContext(r.Context()) {
		keyScope := auth.ScopeFromContext(r.Context())
		if keyScope != nil && !auth.ScopeIsSubset(keyScope, req.Scope) {
			ErrorWithCode(w, http.StatusForbidden, "scope_violation",
				"enrollment token scope must be a subset of the API key's scope")
			return
		}
	}

	// Tenant validation: every group/tag/site/device ID in scope must belong
	// to the operator's tenant. The subset check above only protects against
	// scope expansion within a tenant; without this, an admin could embed a
	// foreign-tenant ID and pollute device_groups with cross-tenant rows.
	if err := auth.ValidateScopeTenant(r.Context(), h.pool, tenantID, req.Scope); err != nil {
		ErrorWithCode(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}

	expiry := 24 * time.Hour
	if req.ExpiresInSeconds > 0 {
		expiry = time.Duration(req.ExpiresInSeconds) * time.Second
	}

	result, err := h.enrollment.CreateToken(r.Context(), tenantID, userID, req.Scope, expiry)
	if err != nil {
		h.log.Error("failed to create enrollment token", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create enrollment token")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"enrollment_token.create", "enrollment_token", result.Token.ID, nil)
	}

	JSON(w, http.StatusCreated, createTokenResponse{
		ID:        result.Token.ID,
		Token:     result.Raw,
		ExpiresAt: result.Token.ExpiresAt,
		Scope:     result.Token.Scope,
	})
}

type tokenListItem struct {
	ID        string           `json:"id"`
	CreatedBy string           `json:"created_by"`
	Scope     *models.APIScope `json:"scope,omitempty"`
	UsedAt    *time.Time       `json:"used_at,omitempty"`
	ExpiresAt time.Time        `json:"expires_at"`
	CreatedAt time.Time        `json:"created_at"`
}

// List handles GET /v1/enrollment-tokens.
func (h *EnrollmentTokensHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()

	rows, err := h.pool.Query(ctx,
		`SELECT id, created_by, scope, used_at, expires_at, created_at
		 FROM enrollment_tokens
		 WHERE tenant_id = $1
		 ORDER BY created_at DESC LIMIT 100`, tenantID)
	if err != nil {
		h.log.Error("failed to list enrollment tokens", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list enrollment tokens")
		return
	}
	defer rows.Close()

	var tokens []tokenListItem
	for rows.Next() {
		var t tokenListItem
		var scopeJSON []byte
		if err := rows.Scan(&t.ID, &t.CreatedBy, &scopeJSON, &t.UsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
			h.log.Error("failed to scan token", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to list enrollment tokens")
			return
		}
		if scopeJSON != nil {
			var scope models.APIScope
			if err := json.Unmarshal(scopeJSON, &scope); err == nil {
				t.Scope = &scope
			}
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		Error(w, http.StatusInternalServerError, "failed to list enrollment tokens")
		return
	}

	if tokens == nil {
		tokens = []tokenListItem{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": tokens})
}

// Delete handles DELETE /v1/enrollment-tokens/{token_id}.
func (h *EnrollmentTokensHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	tokenID := chi.URLParam(r, "token_id")

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM enrollment_tokens WHERE id = $1 AND tenant_id = $2`, tokenID, tenantID)
	if err != nil {
		h.log.Error("failed to delete enrollment token", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to delete enrollment token")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "enrollment token not found")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"enrollment_token.delete", "enrollment_token", tokenID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}
