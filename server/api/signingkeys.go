package api

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// SigningKeysHandler manages signing key CRUD.
type SigningKeysHandler struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
}

// NewSigningKeysHandler creates a SigningKeysHandler.
func NewSigningKeysHandler(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *SigningKeysHandler {
	return &SigningKeysHandler{pool: pool, audit: auditLog, log: log}
}

type createSigningKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"` // PEM-encoded Ed25519
}

// Create handles POST /v1/signing-keys.
func (h *SigningKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req createSigningKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.PublicKey == "" {
		Error(w, http.StatusBadRequest, "public_key is required")
		return
	}

	// Parse and validate the PEM-encoded Ed25519 public key
	fingerprint, err := validateEd25519PEM(req.PublicKey)
	if err != nil {
		ErrorWithCode(w, http.StatusBadRequest, "invalid_key", err.Error())
		return
	}

	now := time.Now().UTC()
	key := models.SigningKey{
		ID:          models.NewSigningKeyID(),
		TenantID:    tenantID,
		Name:        req.Name,
		Algorithm:   "ed25519",
		PublicKey:   req.PublicKey,
		Fingerprint: fingerprint,
		CreatedBy:   userID,
		CreatedAt:   now,
	}

	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO signing_keys (id, tenant_id, name, algorithm, public_key, fingerprint, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		key.ID, key.TenantID, key.Name, key.Algorithm, key.PublicKey, key.Fingerprint, key.CreatedBy, key.CreatedAt,
	)
	if err != nil {
		h.log.Error("insert signing key", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create signing key")
		return
	}

	h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"signing_key.create", "signing_key", key.ID, nil)

	JSON(w, http.StatusCreated, key)
}

// List handles GET /v1/signing-keys.
func (h *SigningKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	rows, err := h.pool.Query(r.Context(),
		`SELECT id, tenant_id, name, algorithm, public_key, fingerprint, created_by, created_at
		 FROM signing_keys WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		h.log.Error("list signing keys", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list signing keys")
		return
	}
	defer rows.Close()

	var keys []models.SigningKey
	for rows.Next() {
		var k models.SigningKey
		if err := rows.Scan(&k.ID, &k.TenantID, &k.Name, &k.Algorithm,
			&k.PublicKey, &k.Fingerprint, &k.CreatedBy, &k.CreatedAt); err != nil {
			h.log.Error("scan signing key", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to read signing keys")
			return
		}
		keys = append(keys, k)
	}

	if keys == nil {
		keys = []models.SigningKey{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": keys})
}

// Delete handles DELETE /v1/signing-keys/{key_id}.
func (h *SigningKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	keyID := chi.URLParam(r, "key_id")

	// Check if referenced by files
	var refCount int
	err := h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM files WHERE signature_key_id = $1 AND tenant_id = $2`,
		keyID, tenantID).Scan(&refCount)
	if err != nil {
		h.log.Error("check signing key refs", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to check key references")
		return
	}
	if refCount > 0 {
		ErrorWithCode(w, http.StatusConflict, "key_in_use",
			fmt.Sprintf("signing key is referenced by %d file(s)", refCount))
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM signing_keys WHERE id = $1 AND tenant_id = $2`, keyID, tenantID)
	if err != nil {
		h.log.Error("delete signing key", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to delete signing key")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "signing key not found")
		return
	}

	h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"signing_key.delete", "signing_key", keyID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// validateEd25519PEM parses a PEM-encoded Ed25519 public key
// and returns its SHA-256 fingerprint.
func validateEd25519PEM(pemData string) (string, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return "", fmt.Errorf("invalid PEM data")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("invalid public key: %w", err)
	}

	if _, ok := pub.(ed25519.PublicKey); !ok {
		return "", fmt.Errorf("key is not Ed25519")
	}

	hash := sha256.Sum256(block.Bytes)
	return fmt.Sprintf("SHA256:%x", hash), nil
}
