package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// AgentVersionsHandler manages the agent version registry.
type AgentVersionsHandler struct {
	store *store.Store
	audit *audit.Logger
	log   *slog.Logger
}

// NewAgentVersionsHandler creates an AgentVersionsHandler.
func NewAgentVersionsHandler(st *store.Store, auditLog *audit.Logger, log *slog.Logger) *AgentVersionsHandler {
	return &AgentVersionsHandler{store: st, audit: auditLog, log: log}
}

type publishVersionRequest struct {
	Version   string                   `json:"version"`
	Channel   string                   `json:"channel"`
	Changelog string                   `json:"changelog,omitempty"`
	Binaries  []publishVersionBinaryIn `json:"binaries"`
}

type publishVersionBinaryIn struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	FileID         string `json:"file_id"`
	SHA256         string `json:"sha256"`
	Signature      string `json:"signature"`
	SignatureKeyID string `json:"signature_key_id"`
}

// Create handles POST /v1/agent-versions.
func (h *AgentVersionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req publishVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Version == "" {
		Error(w, http.StatusBadRequest, "version is required")
		return
	}
	if req.Channel == "" {
		Error(w, http.StatusBadRequest, "channel is required")
		return
	}
	if req.Channel != models.ChannelStable && req.Channel != models.ChannelBeta && req.Channel != models.ChannelCanary {
		Error(w, http.StatusBadRequest, "channel must be stable, beta, or canary")
		return
	}
	if len(req.Binaries) == 0 {
		Error(w, http.StatusBadRequest, "at least one binary is required")
		return
	}

	for _, b := range req.Binaries {
		if b.OS == "" || b.Arch == "" || b.FileID == "" || b.SHA256 == "" || b.Signature == "" || b.SignatureKeyID == "" {
			Error(w, http.StatusBadRequest, "each binary requires os, arch, file_id, sha256, signature, and signature_key_id")
			return
		}
	}

	// Check for duplicate version
	existing, err := h.store.GetAgentVersion(r.Context(), req.Version)
	if err != nil {
		h.log.Error("check existing version", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to check version")
		return
	}
	if existing != nil {
		ErrorWithCode(w, http.StatusConflict, "version_exists", "version already exists")
		return
	}

	now := time.Now().UTC()
	v := models.AgentVersion{
		ID:        models.NewAgentVersionID(),
		Version:   req.Version,
		Channel:   req.Channel,
		Changelog: req.Changelog,
		CreatedAt: now,
	}

	for _, b := range req.Binaries {
		v.Binaries = append(v.Binaries, models.AgentVersionBinary{
			ID:             models.NewAgentVersionBinaryID(),
			AgentVersionID: v.ID,
			OS:             b.OS,
			Arch:           b.Arch,
			FileID:         b.FileID,
			SHA256:         b.SHA256,
			Signature:      b.Signature,
			SignatureKeyID: b.SignatureKeyID,
		})
	}

	if err := h.store.CreateAgentVersion(r.Context(), &v); err != nil {
		h.log.Error("create agent version", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to publish version")
		return
	}

	h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"agent_version.publish", "agent_version", v.ID, map[string]any{
			"version": v.Version,
			"channel": v.Channel,
		})

	JSON(w, http.StatusCreated, v)
}

// List handles GET /v1/agent-versions.
func (h *AgentVersionsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, cursor := ParsePagination(r)
	channel := r.URL.Query().Get("channel")

	versions, err := h.store.ListAgentVersions(r.Context(), channel, cursor, limit+1)
	if err != nil {
		h.log.Error("list agent versions", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list versions")
		return
	}

	hasMore := len(versions) > limit
	if hasMore {
		versions = versions[:limit]
	}

	nextCursor := ""
	if hasMore && len(versions) > 0 {
		nextCursor = versions[len(versions)-1].ID
	}

	JSON(w, http.StatusOK, ListResponse{
		Data: versions,
		Pagination: Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			Limit:      limit,
		},
	})
}

// Get handles GET /v1/agent-versions/{version}.
func (h *AgentVersionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ver := chi.URLParam(r, "version")
	v, err := h.store.GetAgentVersion(r.Context(), ver)
	if err != nil {
		h.log.Error("get agent version", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get version")
		return
	}
	if v == nil {
		Error(w, http.StatusNotFound, "version not found")
		return
	}

	JSON(w, http.StatusOK, v)
}

// Yank handles POST /v1/agent-versions/{version}/yank.
func (h *AgentVersionsHandler) Yank(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	ver := chi.URLParam(r, "version")

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.store.YankAgentVersion(r.Context(), ver, req.Reason); err != nil {
		h.log.Error("yank agent version", slog.String("error", err.Error()))
		Error(w, http.StatusNotFound, "version not found")
		return
	}

	h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
		"agent_version.yank", "agent_version", ver, map[string]any{
			"reason": req.Reason,
		})

	w.WriteHeader(http.StatusNoContent)
}
