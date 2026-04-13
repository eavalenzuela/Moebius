package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/server/auth"
)

// AgentSigningKeysHandler serves signing key public material to agents (mTLS-authenticated).
type AgentSigningKeysHandler struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewAgentSigningKeysHandler creates an AgentSigningKeysHandler.
func NewAgentSigningKeysHandler(pool *pgxpool.Pool, log *slog.Logger) *AgentSigningKeysHandler {
	return &AgentSigningKeysHandler{pool: pool, log: log}
}

// Get handles GET /v1/agents/signing-keys/{key_id}. The mTLS middleware
// resolves the agent's tenant, and the lookup is scoped to that tenant so
// a foreign-tenant key ID returns 404 rather than leaking the public key.
func (h *AgentSigningKeysHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		Error(w, http.StatusUnauthorized, "tenant not resolved")
		return
	}
	keyID := chi.URLParam(r, "key_id")

	var publicKey string
	err := h.pool.QueryRow(r.Context(),
		`SELECT public_key FROM signing_keys WHERE id = $1 AND tenant_id = $2`,
		keyID, tenantID,
	).Scan(&publicKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "signing key not found")
			return
		}
		h.log.Error("fetch signing key", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to fetch signing key")
		return
	}

	JSON(w, http.StatusOK, map[string]string{"public_key": publicKey})
}
