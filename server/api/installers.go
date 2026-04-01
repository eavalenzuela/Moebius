package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/storage"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// InstallersHandler serves installer hosting endpoints per INSTALLER_PACKAGING_SPEC.md.
type InstallersHandler struct {
	pool       *pgxpool.Pool
	storage    storage.Backend
	enrollment *auth.EnrollmentService
	audit      *audit.Logger
	log        *slog.Logger
}

// NewInstallersHandler creates an InstallersHandler.
func NewInstallersHandler(pool *pgxpool.Pool, st storage.Backend, enrollment *auth.EnrollmentService, auditLog *audit.Logger, log *slog.Logger) *InstallersHandler {
	return &InstallersHandler{
		pool:       pool,
		storage:    st,
		enrollment: enrollment,
		audit:      auditLog,
		log:        log,
	}
}

// ─── Response types ──────────────────────────────────────

type installerListItem struct {
	ID             string    `json:"id"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	Version        string    `json:"version"`
	Channel        string    `json:"channel"`
	SHA256         string    `json:"sha256"`
	SignatureKeyID string    `json:"signature_key_id"`
	DownloadURL    string    `json:"download_url"`
	ReleasedAt     time.Time `json:"released_at"`
	Yanked         bool      `json:"yanked"`
	YankReason     *string   `json:"yank_reason,omitempty"`
}

type createInstallerRequest struct {
	Version        string `json:"version"`
	Channel        string `json:"channel"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	FileID         string `json:"file_id"`
	SHA256         string `json:"sha256"`
	Signature      string `json:"signature"`
	SignatureKeyID string `json:"signature_key_id"`
}

type installerRecord struct {
	ID             string
	Version        string
	FileID         string
	SHA256         string
	Signature      string
	SignatureKeyID string
}

// ─── List (API key auth) ─────────────────────────────────

// List handles GET /v1/installers — list all available (non-yanked) installers.
func (h *InstallersHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, os, arch, version, channel, sha256, signature_key_id,
		        released_at, yanked, yank_reason
		 FROM installers
		 WHERE yanked = false
		 ORDER BY released_at DESC
		 LIMIT 100`)
	if err != nil {
		h.log.Error("list installers", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list installers")
		return
	}
	defer rows.Close()

	var items []installerListItem
	for rows.Next() {
		var it installerListItem
		if err := rows.Scan(&it.ID, &it.OS, &it.Arch, &it.Version, &it.Channel,
			&it.SHA256, &it.SignatureKeyID, &it.ReleasedAt,
			&it.Yanked, &it.YankReason); err != nil {
			h.log.Error("scan installer", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to list installers")
			return
		}
		it.DownloadURL = "/v1/installers/" + it.OS + "/" + it.Arch + "/" + it.Version
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		Error(w, http.StatusInternalServerError, "failed to list installers")
		return
	}
	if items == nil {
		items = []installerListItem{}
	}

	JSON(w, http.StatusOK, map[string]any{"installers": items})
}

// ─── Create (API key auth, admin) ────────────────────────

// Create handles POST /v1/installers — register a new installer version.
// The installer binary must already be uploaded via the file upload API.
func (h *InstallersHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req createInstallerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Version == "" || req.Channel == "" || req.OS == "" || req.Arch == "" {
		Error(w, http.StatusBadRequest, "version, channel, os, and arch are required")
		return
	}
	if req.FileID == "" || req.SHA256 == "" || req.Signature == "" || req.SignatureKeyID == "" {
		Error(w, http.StatusBadRequest, "file_id, sha256, signature, and signature_key_id are required")
		return
	}
	if !isValidOS(req.OS) {
		Error(w, http.StatusBadRequest, "invalid os: must be linux or windows")
		return
	}
	if !isValidArch(req.OS, req.Arch) {
		Error(w, http.StatusBadRequest, "invalid arch for os")
		return
	}

	// Verify the referenced file exists.
	var fileExists bool
	if err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM files WHERE id = $1)`, req.FileID,
	).Scan(&fileExists); err != nil || !fileExists {
		Error(w, http.StatusBadRequest, "file_id does not reference an existing file")
		return
	}

	// Verify the signing key exists.
	var keyExists bool
	if err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signing_keys WHERE id = $1)`, req.SignatureKeyID,
	).Scan(&keyExists); err != nil || !keyExists {
		Error(w, http.StatusBadRequest, "signature_key_id does not reference an existing signing key")
		return
	}

	id := models.NewInstallerID()
	now := time.Now().UTC()

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO installers (id, version, channel, os, arch, file_id, sha256, signature, signature_key_id, released_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, req.Version, req.Channel, req.OS, req.Arch,
		req.FileID, req.SHA256, req.Signature, req.SignatureKeyID, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "duplicate key") {
			Error(w, http.StatusConflict, "installer already exists for this version/os/arch")
			return
		}
		h.log.Error("create installer", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create installer")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"installer.create", "installer", id, map[string]any{
				"version": req.Version, "os": req.OS, "arch": req.Arch,
			})
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":          id,
		"version":     req.Version,
		"channel":     req.Channel,
		"os":          req.OS,
		"arch":        req.Arch,
		"released_at": now,
	})
}

// ─── Download (API key OR enrollment token) ──────────────

// Download handles GET /v1/installers/{os}/{arch}/{version}.
func (h *InstallersHandler) Download(w http.ResponseWriter, r *http.Request) {
	if !h.checkInstallerAuth(w, r) {
		return
	}

	rec, ok := h.lookupInstaller(w, r, chi.URLParam(r, "os"), chi.URLParam(r, "arch"), chi.URLParam(r, "version"))
	if !ok {
		return
	}
	h.serveFile(w, r, rec.FileID, installerFilename(chi.URLParam(r, "os"), chi.URLParam(r, "arch"), rec.Version))
}

// DownloadLatest handles GET /v1/installers/{os}/{arch}/latest.
func (h *InstallersHandler) DownloadLatest(w http.ResponseWriter, r *http.Request) {
	if !h.checkInstallerAuth(w, r) {
		return
	}

	targetOS := chi.URLParam(r, "os")
	targetArch := chi.URLParam(r, "arch")

	var fileID, version string
	err := h.pool.QueryRow(r.Context(),
		`SELECT file_id, version FROM installers
		 WHERE os = $1 AND arch = $2 AND channel = 'stable' AND yanked = false
		 ORDER BY released_at DESC LIMIT 1`,
		targetOS, targetArch,
	).Scan(&fileID, &version)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "no stable installer available for this platform")
			return
		}
		h.log.Error("download latest installer", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to look up installer")
		return
	}

	h.serveFile(w, r, fileID, installerFilename(targetOS, targetArch, version))
}

// ─── Checksum / Signature (API key OR enrollment token) ──

// Checksum handles GET /v1/installers/{os}/{arch}/{version}/checksum.
func (h *InstallersHandler) Checksum(w http.ResponseWriter, r *http.Request) {
	if !h.checkInstallerAuth(w, r) {
		return
	}

	targetOS := chi.URLParam(r, "os")
	targetArch := chi.URLParam(r, "arch")
	rec, ok := h.lookupInstaller(w, r, targetOS, targetArch, chi.URLParam(r, "version"))
	if !ok {
		return
	}

	fname := installerFilename(targetOS, targetArch, rec.Version)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// sha256sum-compatible format: "<hash>  <filename>\n"
	_, _ = w.Write([]byte(rec.SHA256 + "  " + fname + "\n")) //nolint:gosec // text/plain checksum response, not HTML
}

// Signature handles GET /v1/installers/{os}/{arch}/{version}/signature.
func (h *InstallersHandler) Signature(w http.ResponseWriter, r *http.Request) {
	if !h.checkInstallerAuth(w, r) {
		return
	}

	rec, ok := h.lookupInstaller(w, r, chi.URLParam(r, "os"), chi.URLParam(r, "arch"), chi.URLParam(r, "version"))
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Signature-Key-ID", rec.SignatureKeyID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rec.Signature))
}

// ─── Internal helpers ────────────────────────────────────

// lookupInstaller queries by os/arch/version. Returns false and writes an error if not found.
func (h *InstallersHandler) lookupInstaller(w http.ResponseWriter, r *http.Request, targetOS, targetArch, version string) (*installerRecord, bool) {
	var rec installerRecord
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, version, file_id, sha256, signature, signature_key_id
		 FROM installers
		 WHERE os = $1 AND arch = $2 AND version = $3 AND yanked = false`,
		targetOS, targetArch, version,
	).Scan(&rec.ID, &rec.Version, &rec.FileID, &rec.SHA256, &rec.Signature, &rec.SignatureKeyID)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "installer not found")
		} else {
			h.log.Error("lookup installer", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to look up installer")
		}
		return nil, false
	}
	return &rec, true
}

// serveFile opens a file from storage and streams it to the client.
func (h *InstallersHandler) serveFile(w http.ResponseWriter, r *http.Request, fileID, filename string) {
	rc, err := h.storage.Open(r.Context(), fileID)
	if err != nil {
		h.log.Error("open installer file", slog.String("file_id", fileID), slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to retrieve installer file")
		return
	}
	defer func() { _ = rc.Close() }()

	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	// If storage returns an *os.File, use http.ServeContent for Range support.
	if f, ok := rc.(*os.File); ok {
		stat, _ := f.Stat()
		http.ServeContent(w, r, filename, stat.ModTime(), f)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, rc)
}

// checkInstallerAuth validates that the request carries a valid API key or
// enrollment token in the Authorization header. Per the spec, installer
// downloads require authentication but accept either credential type.
// Returns true if auth succeeded; on failure, writes a 401 and returns false.
func (h *InstallersHandler) checkInstallerAuth(w http.ResponseWriter, r *http.Request) bool {
	bearer := r.Header.Get("Authorization")
	if bearer == "" {
		Error(w, http.StatusUnauthorized, "authentication required: provide an API key or enrollment token")
		return false
	}

	token := strings.TrimPrefix(bearer, "Bearer ")
	if token == bearer {
		// No "Bearer " prefix — try without.
		token = bearer
	}

	// Try as API key (sk_ prefix).
	if strings.HasPrefix(token, "sk_") {
		var exists bool
		err := h.pool.QueryRow(r.Context(),
			`SELECT EXISTS(
				SELECT 1 FROM api_keys
				WHERE key_hash = encode(sha256($1::bytea), 'hex')
				AND revoked_at IS NULL
			)`, token,
		).Scan(&exists)
		if err == nil && exists {
			return true
		}
	}

	// Try as enrollment token (peek — don't consume).
	if _, err := h.enrollment.Peek(r.Context(), token); err == nil {
		return true
	}

	Error(w, http.StatusUnauthorized, "invalid API key or enrollment token")
	return false
}

func installerFilename(targetOS, targetArch, version string) string {
	if targetOS == "windows" {
		return "agent-windows-" + targetArch + "-" + version + ".msi"
	}
	return "agent-" + targetOS + "-" + targetArch + "-" + version + ".tar.gz"
}
