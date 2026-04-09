package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/storage"
	"github.com/eavalenzuela/Moebius/shared/models"
)

const (
	defaultChunkSize = 5 * 1024 * 1024 // 5 MB
	uploadExpiry     = 24 * time.Hour
)

// FilesHandler manages file uploads, metadata, and downloads.
type FilesHandler struct {
	pool    *pgxpool.Pool
	storage storage.Backend
	audit   *audit.Logger
	log     *slog.Logger
	tempDir string // where chunks are staged before assembly
}

// NewFilesHandler creates a FilesHandler.
func NewFilesHandler(pool *pgxpool.Pool, store storage.Backend, auditLog *audit.Logger, log *slog.Logger) *FilesHandler {
	tempDir := filepath.Join(os.TempDir(), "moebius-uploads")
	_ = os.MkdirAll(tempDir, 0o750)
	return &FilesHandler{
		pool:    pool,
		storage: store,
		audit:   auditLog,
		log:     log,
		tempDir: tempDir,
	}
}

// ─── Initiate Upload ───────────────────────────────────

type initiateUploadRequest struct {
	Filename       string `json:"filename"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
	Signature      string `json:"signature,omitempty"`
	SignatureKeyID string `json:"signature_key_id,omitempty"`
	MIMEType       string `json:"mime_type,omitempty"`
}

type initiateUploadResponse struct {
	FileID         string    `json:"file_id"`
	UploadID       string    `json:"upload_id"`
	ChunkSizeBytes int       `json:"chunk_size_bytes"`
	TotalChunks    int       `json:"total_chunks"`
	UploadedChunks []int     `json:"uploaded_chunks"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// InitiateUpload handles POST /v1/files.
func (h *FilesHandler) InitiateUpload(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req initiateUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Filename == "" || req.SizeBytes <= 0 || req.SHA256 == "" {
		Error(w, http.StatusBadRequest, "filename, size_bytes, and sha256 are required")
		return
	}

	totalChunks := int(math.Ceil(float64(req.SizeBytes) / float64(defaultChunkSize)))
	now := time.Now().UTC()
	fileID := models.NewFileID()
	uploadID := models.NewUploadID()

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("begin tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to initiate upload")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO files (id, tenant_id, filename, size_bytes, sha256, signature, signature_key_id,
			signature_verified, mime_type, storage_backend, storage_path, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE, $8, $9, '', $10, $11)`,
		fileID, tenantID, req.Filename, req.SizeBytes, req.SHA256,
		nullIfEmpty(req.Signature), nullIfEmpty(req.SignatureKeyID),
		req.MIMEType, h.storage.Name(), userID, now,
	)
	if err != nil {
		h.log.Error("insert file", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create file record")
		return
	}

	expiresAt := now.Add(uploadExpiry)
	_, err = tx.Exec(ctx,
		`INSERT INTO file_uploads (id, file_id, tenant_id, chunk_size_bytes, total_chunks,
			uploaded_chunks, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, '{}', $6, $7)`,
		uploadID, fileID, tenantID, defaultChunkSize, totalChunks, expiresAt, now,
	)
	if err != nil {
		h.log.Error("insert upload", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create upload session")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("commit tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to initiate upload")
		return
	}

	// Create chunk staging directory
	_ = os.MkdirAll(filepath.Join(h.tempDir, uploadID), 0o750)

	JSON(w, http.StatusCreated, initiateUploadResponse{
		FileID:         fileID,
		UploadID:       uploadID,
		ChunkSizeBytes: defaultChunkSize,
		TotalChunks:    totalChunks,
		UploadedChunks: []int{},
		ExpiresAt:      expiresAt,
	})
}

// ─── Upload Chunk ──────────────────────────────────────

type chunkResponse struct {
	ChunkIndex      int   `json:"chunk_index"`
	Received        bool  `json:"received"`
	UploadedChunks  []int `json:"uploaded_chunks"`
	RemainingChunks int   `json:"remaining_chunks"`
}

// UploadChunk handles PUT /v1/files/uploads/{upload_id}/chunks/{chunk_index}.
func (h *FilesHandler) UploadChunk(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	uploadID := chi.URLParam(r, "upload_id")
	chunkIndexStr := chi.URLParam(r, "chunk_index")

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid chunk_index")
		return
	}

	ctx := r.Context()

	// Load upload session
	var fileID string
	var totalChunks int
	var uploadedChunks []int
	var expiresAt time.Time
	err = h.pool.QueryRow(ctx,
		`SELECT file_id, total_chunks, uploaded_chunks, expires_at
		 FROM file_uploads WHERE id = $1 AND tenant_id = $2 AND completed_at IS NULL`,
		uploadID, tenantID,
	).Scan(&fileID, &totalChunks, &uploadedChunks, &expiresAt)
	if err != nil {
		Error(w, http.StatusNotFound, "upload session not found or already completed")
		return
	}

	if time.Now().UTC().After(expiresAt) {
		Error(w, http.StatusGone, "upload session expired")
		return
	}

	if chunkIndex < 0 || chunkIndex >= totalChunks {
		Error(w, http.StatusBadRequest, fmt.Sprintf("chunk_index must be 0-%d", totalChunks-1))
		return
	}

	// Read chunk body and verify SHA-256.
	// http.MaxBytesReader (vs io.LimitReader) returns a real error when the
	// caller exceeds the cap instead of silently truncating, so an oversized
	// chunk surfaces as a 400 here rather than a confusing checksum mismatch.
	// The router-level MaxBytes(MaxBodyBytesFileChunk) provides the outer
	// guard; this inner cap pins the limit to defaultChunkSize+1 specifically.
	expectedHash := r.Header.Get("X-Chunk-SHA256")
	r.Body = http.MaxBytesReader(w, r.Body, int64(defaultChunkSize)+1)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		Error(w, http.StatusBadRequest, "chunk exceeds maximum size")
		return
	}

	if expectedHash != "" {
		actualHash := sha256.Sum256(body)
		if hex.EncodeToString(actualHash[:]) != expectedHash {
			ErrorWithCode(w, http.StatusBadRequest, "checksum_mismatch", "chunk SHA-256 does not match")
			return
		}
	}

	// Write chunk to staging
	chunkPath := filepath.Join(h.tempDir, uploadID, fmt.Sprintf("%d", chunkIndex))
	if err := os.WriteFile(chunkPath, body, 0o600); err != nil { //nolint:gosec // server-controlled path from upload_id + chunk_index
		h.log.Error("write chunk", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to write chunk")
		return
	}

	// Add chunk index to uploaded_chunks (idempotent)
	_, err = h.pool.Exec(ctx,
		`UPDATE file_uploads SET uploaded_chunks = array_append(
			array_remove(uploaded_chunks, $1), $1),
			expires_at = $2
		 WHERE id = $3`,
		chunkIndex, time.Now().UTC().Add(uploadExpiry), uploadID,
	)
	if err != nil {
		h.log.Error("update uploaded_chunks", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to update upload session")
		return
	}

	// Re-read updated chunks
	_ = h.pool.QueryRow(ctx,
		`SELECT uploaded_chunks FROM file_uploads WHERE id = $1`, uploadID,
	).Scan(&uploadedChunks)

	JSON(w, http.StatusOK, chunkResponse{
		ChunkIndex:      chunkIndex,
		Received:        true,
		UploadedChunks:  uploadedChunks,
		RemainingChunks: totalChunks - len(uploadedChunks),
	})
}

// ─── Upload Status ─────────────────────────────────────

type uploadStatusResponse struct {
	UploadID       string    `json:"upload_id"`
	FileID         string    `json:"file_id"`
	TotalChunks    int       `json:"total_chunks"`
	UploadedChunks []int     `json:"uploaded_chunks"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// UploadStatus handles GET /v1/files/uploads/{upload_id}.
func (h *FilesHandler) UploadStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	uploadID := chi.URLParam(r, "upload_id")

	var resp uploadStatusResponse
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, file_id, total_chunks, uploaded_chunks, expires_at
		 FROM file_uploads WHERE id = $1 AND tenant_id = $2 AND completed_at IS NULL`,
		uploadID, tenantID,
	).Scan(&resp.UploadID, &resp.FileID, &resp.TotalChunks, &resp.UploadedChunks, &resp.ExpiresAt)
	if err != nil {
		Error(w, http.StatusNotFound, "upload session not found")
		return
	}
	if resp.UploadedChunks == nil {
		resp.UploadedChunks = []int{}
	}
	JSON(w, http.StatusOK, resp)
}

// ─── Complete Upload ───────────────────────────────────

// CompleteUpload handles POST /v1/files/uploads/{upload_id}/complete.
func (h *FilesHandler) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	uploadID := chi.URLParam(r, "upload_id")
	ctx := r.Context()

	var fileID string
	var totalChunks int
	var uploadedChunks []int
	err := h.pool.QueryRow(ctx,
		`SELECT file_id, total_chunks, uploaded_chunks
		 FROM file_uploads WHERE id = $1 AND tenant_id = $2 AND completed_at IS NULL`,
		uploadID, tenantID,
	).Scan(&fileID, &totalChunks, &uploadedChunks)
	if err != nil {
		Error(w, http.StatusNotFound, "upload session not found or already completed")
		return
	}

	if len(uploadedChunks) != totalChunks {
		ErrorWithCode(w, http.StatusBadRequest, "incomplete_upload",
			fmt.Sprintf("uploaded %d of %d chunks", len(uploadedChunks), totalChunks))
		return
	}

	// Assemble chunks and verify SHA-256
	var expectedSHA string
	_ = h.pool.QueryRow(ctx, `SELECT sha256 FROM files WHERE id = $1`, fileID).Scan(&expectedSHA)

	hasher := sha256.New()
	stageDir := filepath.Join(h.tempDir, uploadID)

	// Write assembled file to storage via pipe
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	var storagePath string

	go func() {
		var saveErr error
		storagePath, saveErr = h.storage.Save(ctx, fileID, pr)
		errCh <- saveErr
	}()

	for i := 0; i < totalChunks; i++ {
		chunkPath := filepath.Join(stageDir, fmt.Sprintf("%d", i))
		data, readErr := os.ReadFile(chunkPath) //nolint:gosec // server-controlled path
		if readErr != nil {
			_ = pw.CloseWithError(readErr)
			Error(w, http.StatusInternalServerError, "failed to read chunk")
			return
		}
		_, _ = hasher.Write(data)
		if _, writeErr := pw.Write(data); writeErr != nil {
			_ = pw.CloseWithError(writeErr)
			Error(w, http.StatusInternalServerError, "failed to assemble file")
			return
		}
	}
	_ = pw.Close()

	if saveErr := <-errCh; saveErr != nil {
		h.log.Error("save assembled file", slog.String("error", saveErr.Error()))
		Error(w, http.StatusInternalServerError, "failed to store file")
		return
	}

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if actualSHA != expectedSHA {
		_ = h.storage.Delete(ctx, fileID)
		ErrorWithCode(w, http.StatusBadRequest, "checksum_mismatch",
			"assembled file SHA-256 does not match expected hash")
		return
	}

	// Update file record with storage path, mark upload completed
	now := time.Now().UTC()
	_, _ = h.pool.Exec(ctx,
		`UPDATE files SET storage_path = $1 WHERE id = $2`, storagePath, fileID)
	_, _ = h.pool.Exec(ctx,
		`UPDATE file_uploads SET completed_at = $1 WHERE id = $2`, now, uploadID)

	// Clean up staging
	_ = os.RemoveAll(stageDir) //nolint:gosec // server-controlled staging directory

	_ = h.audit.LogAction(ctx, tenantID, userID, models.ActorTypeUser,
		"file.upload", "file", fileID, nil)

	// Return file metadata
	var f models.File
	_ = h.pool.QueryRow(ctx,
		`SELECT id, tenant_id, filename, size_bytes, sha256, signature, signature_key_id,
			signature_verified, mime_type, storage_backend, storage_path, created_by, created_at
		 FROM files WHERE id = $1`, fileID,
	).Scan(&f.ID, &f.TenantID, &f.Filename, &f.SizeBytes, &f.SHA256,
		&f.Signature, &f.SignatureKeyID, &f.SignatureVerified,
		&f.MIMEType, &f.StorageBackend, &f.StoragePath, &f.CreatedBy, &f.CreatedAt)

	JSON(w, http.StatusOK, f)
}

// ─── List/Get/Delete Files ─────────────────────────────

// ListFiles handles GET /v1/files.
func (h *FilesHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	limit, cursor := ParsePagination(r)
	search := r.URL.Query().Get("search")

	query := `SELECT id, tenant_id, filename, size_bytes, sha256, signature, signature_key_id,
		signature_verified, mime_type, storage_backend, storage_path, created_by, created_at
		FROM files WHERE tenant_id = $1`
	args := []any{tenantID}
	argN := 2

	if search != "" {
		query += fmt.Sprintf(" AND filename ILIKE '%%' || $%d || '%%'", argN)
		args = append(args, search)
		argN++
	}

	if cursor != "" {
		query += fmt.Sprintf(" AND id < $%d", argN)
		args = append(args, cursor)
		argN++
	}

	query += " ORDER BY created_at DESC, id DESC"
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit+1)

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		h.log.Error("list files", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list files")
		return
	}
	defer rows.Close()

	var files []models.File
	for rows.Next() {
		var f models.File
		if err := rows.Scan(&f.ID, &f.TenantID, &f.Filename, &f.SizeBytes, &f.SHA256,
			&f.Signature, &f.SignatureKeyID, &f.SignatureVerified,
			&f.MIMEType, &f.StorageBackend, &f.StoragePath, &f.CreatedBy, &f.CreatedAt); err != nil {
			h.log.Error("scan file", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to read files")
			return
		}
		files = append(files, f)
	}

	hasMore := len(files) > limit
	if hasMore {
		files = files[:limit]
	}

	var nextCursor string
	if hasMore && len(files) > 0 {
		nextCursor = files[len(files)-1].ID
	}

	if files == nil {
		files = []models.File{}
	}

	JSON(w, http.StatusOK, ListResponse{
		Data: files,
		Pagination: Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			Limit:      limit,
		},
	})
}

// GetFile handles GET /v1/files/{file_id}.
func (h *FilesHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	fileID := chi.URLParam(r, "file_id")

	var f models.File
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, tenant_id, filename, size_bytes, sha256, signature, signature_key_id,
			signature_verified, mime_type, storage_backend, storage_path, created_by, created_at
		 FROM files WHERE id = $1 AND tenant_id = $2`, fileID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.Filename, &f.SizeBytes, &f.SHA256,
		&f.Signature, &f.SignatureKeyID, &f.SignatureVerified,
		&f.MIMEType, &f.StorageBackend, &f.StoragePath, &f.CreatedBy, &f.CreatedAt)
	if err != nil {
		Error(w, http.StatusNotFound, "file not found")
		return
	}

	JSON(w, http.StatusOK, f)
}

// DeleteFile handles DELETE /v1/files/{file_id}.
func (h *FilesHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	fileID := chi.URLParam(r, "file_id")
	ctx := r.Context()

	// Check for active transfer jobs referencing this file
	var activeJobs int
	_ = h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM jobs
		 WHERE tenant_id = $1 AND type = 'file_transfer'
		   AND status NOT IN ('completed', 'failed', 'timed_out', 'cancelled')
		   AND payload->>'file_id' = $2`,
		tenantID, fileID,
	).Scan(&activeJobs)
	if activeJobs > 0 {
		ErrorWithCode(w, http.StatusConflict, "file_in_use",
			fmt.Sprintf("file is referenced by %d active job(s)", activeJobs))
		return
	}

	// Get storage path for backend deletion
	var storagePath string
	err := h.pool.QueryRow(ctx,
		`SELECT storage_path FROM files WHERE id = $1 AND tenant_id = $2`,
		fileID, tenantID,
	).Scan(&storagePath)
	if err != nil {
		Error(w, http.StatusNotFound, "file not found")
		return
	}

	// Delete from DB (cascades to file_uploads)
	tag, err := h.pool.Exec(ctx,
		`DELETE FROM files WHERE id = $1 AND tenant_id = $2`, fileID, tenantID)
	if err != nil {
		h.log.Error("delete file", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to delete file")
		return
	}
	if tag.RowsAffected() == 0 {
		Error(w, http.StatusNotFound, "file not found")
		return
	}

	// Delete from storage backend
	if storagePath != "" {
		_ = h.storage.Delete(ctx, fileID)
	}

	_ = h.audit.LogAction(ctx, tenantID, userID, models.ActorTypeUser,
		"file.delete", "file", fileID, nil)

	w.WriteHeader(http.StatusNoContent)
}

// ─── File Download ─────────────────────────────────────

// Download handles GET /v1/files/{file_id}/download (mTLS for agents).
func (h *FilesHandler) Download(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	fileID := chi.URLParam(r, "file_id")

	var f models.File
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, tenant_id, filename, size_bytes, sha256, signature, signature_key_id,
			signature_verified, mime_type, storage_backend, storage_path, created_by, created_at
		 FROM files WHERE id = $1 AND tenant_id = $2`, fileID, tenantID,
	).Scan(&f.ID, &f.TenantID, &f.Filename, &f.SizeBytes, &f.SHA256,
		&f.Signature, &f.SignatureKeyID, &f.SignatureVerified,
		&f.MIMEType, &f.StorageBackend, &f.StoragePath, &f.CreatedBy, &f.CreatedAt)
	if err != nil {
		Error(w, http.StatusNotFound, "file not found")
		return
	}

	downloadURL, err := h.storage.URL(r.Context(), fileID)
	if err != nil {
		h.log.Error("generate download URL", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to generate download URL")
		return
	}

	totalChunks := int(math.Ceil(float64(f.SizeBytes) / float64(defaultChunkSize)))
	resp := fileDownloadResponse{
		URL:            downloadURL,
		SizeBytes:      f.SizeBytes,
		ChunkSizeBytes: defaultChunkSize,
		TotalChunks:    totalChunks,
		SHA256:         f.SHA256,
		Signature:      f.Signature,
		SignatureKeyID: f.SignatureKeyID,
		ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
	}

	JSON(w, http.StatusOK, resp)
}

type fileDownloadResponse struct {
	URL            string    `json:"url"`
	SizeBytes      int64     `json:"size_bytes"`
	ChunkSizeBytes int       `json:"chunk_size_bytes"`
	TotalChunks    int       `json:"total_chunks"`
	SHA256         string    `json:"sha256"`
	Signature      string    `json:"signature,omitempty"`
	SignatureKeyID string    `json:"signature_key_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// ServeFileData handles GET /v1/files/data/{file_id} (local backend).
// Serves the raw file data with Range request support.
func (h *FilesHandler) ServeFileData(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "file_id")

	rc, err := h.storage.Open(r.Context(), fileID)
	if err != nil {
		Error(w, http.StatusNotFound, "file data not found")
		return
	}
	defer func() { _ = rc.Close() }()

	// If the storage returns an *os.File, we can use http.ServeContent for Range support
	if f, ok := rc.(*os.File); ok {
		stat, _ := f.Stat()
		http.ServeContent(w, r, "", stat.ModTime(), f)
		return
	}

	// Fallback: stream without Range support
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, rc)
}

// nullIfEmpty returns nil for empty strings (for nullable DB columns).
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
