package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
)

// APIKeysHandler handles /v1/api-keys endpoints.
type APIKeysHandler struct {
	store *store.Store
	audit *audit.Logger
}

// NewAPIKeysHandler creates an APIKeysHandler.
func NewAPIKeysHandler(s *store.Store, auditLog *audit.Logger) *APIKeysHandler {
	return &APIKeysHandler{store: s, audit: auditLog}
}

// List handles GET /v1/api-keys.
func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	keys, err := h.store.ListAPIKeys(r.Context(), tenantID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list API keys")
		return
	}
	if keys == nil {
		keys = []models.APIKey{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": keys})
}

type createAPIKeyRequest struct {
	Name      string           `json:"name"`
	RoleID    string           `json:"role_id,omitempty"`
	IsAdmin   bool             `json:"is_admin,omitempty"`
	Scope     *models.APIScope `json:"scope,omitempty"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
}

type createAPIKeyResponse struct {
	models.APIKey
	Key string `json:"key"` // returned only at creation time
}

// Create handles POST /v1/api-keys.
func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	rawKey := generateAPIKey()
	hash := sha256.Sum256([]byte(rawKey))

	key := &models.APIKey{
		ID:        models.NewAPIKeyID(),
		TenantID:  tenantID,
		UserID:    userID,
		Name:      req.Name,
		KeyHash:   hex.EncodeToString(hash[:]),
		RoleID:    req.RoleID,
		Scope:     req.Scope,
		IsAdmin:   req.IsAdmin,
		ExpiresAt: req.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}

	if err := h.store.CreateAPIKey(r.Context(), key); err != nil {
		Error(w, http.StatusInternalServerError, "failed to create API key")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"api_key.create", "api_key", key.ID, map[string]string{
				"name": req.Name,
			})
	}

	JSON(w, http.StatusCreated, createAPIKeyResponse{
		APIKey: *key,
		Key:    rawKey,
	})
}

// Delete handles DELETE /v1/api-keys/{key_id}.
func (h *APIKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	keyID := chi.URLParam(r, "key_id")

	if err := h.store.DeleteAPIKey(r.Context(), tenantID, keyID); err != nil {
		ErrorWithCode(w, http.StatusNotFound, "api_key_not_found", "No API key with the given ID exists")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"api_key.delete", "api_key", keyID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

func generateAPIKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "sk_" + hex.EncodeToString(b)
}
