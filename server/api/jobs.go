package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/jobs"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobsHandler handles /v1/jobs endpoints.
type JobsHandler struct {
	pool  *pgxpool.Pool
	audit *audit.Logger
	log   *slog.Logger
}

// NewJobsHandler creates a JobsHandler.
func NewJobsHandler(pool *pgxpool.Pool, auditLog *audit.Logger, log *slog.Logger) *JobsHandler {
	return &JobsHandler{
		pool:  pool,
		audit: auditLog,
		log:   log,
	}
}

// CreateRequest is the body for POST /v1/jobs.
type CreateRequest struct {
	Type        string              `json:"type"`
	Target      models.JobTarget    `json:"target"`
	Payload     json.RawMessage     `json:"payload"`
	RetryPolicy *models.RetryPolicy `json:"retry_policy,omitempty"`
}

// CreateResponse is the response for POST /v1/jobs.
type CreateResponse struct {
	JobIDs            []string `json:"job_ids"`
	TargetDeviceCount int      `json:"target_device_count"`
}

// Create handles POST /v1/jobs.
func (h *JobsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := jobs.ValidateType(req.Type); err != nil {
		ErrorWithCode(w, http.StatusBadRequest, "invalid_job_type", err.Error())
		return
	}

	if len(req.Payload) == 0 {
		Error(w, http.StatusBadRequest, "payload is required")
		return
	}

	ctx := r.Context()

	// Resolve target devices
	deviceIDs, err := h.resolveTargets(ctx, tenantID, req.Target)
	if err != nil {
		h.log.Error("failed to resolve targets", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to resolve targets")
		return
	}
	if len(deviceIDs) == 0 {
		ErrorWithCode(w, http.StatusBadRequest, "no_targets", "no devices matched the target specification")
		return
	}

	// Scope enforcement: intersect resolved targets with API key scope
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if scope != nil {
			allowed, err := auth.ResolveScope(ctx, h.pool, tenantID, scope)
			if err != nil {
				h.log.Error("failed to resolve scope", slog.String("error", err.Error()))
				Error(w, http.StatusInternalServerError, "failed to resolve scope")
				return
			}
			deviceIDs = auth.FilterDeviceIDs(allowed, deviceIDs)
			if len(deviceIDs) == 0 {
				ErrorWithCode(w, http.StatusForbidden, "scope_violation", "target devices are outside this key's scope")
				return
			}
		}
	}

	// Apply default retry policy if not specified
	retryPolicy := req.RetryPolicy
	if retryPolicy == nil {
		retryPolicy = jobs.DefaultRetryPolicy(req.Type)
	}

	maxRetries := 0
	if retryPolicy != nil {
		maxRetries = retryPolicy.MaxRetries
	}

	var retryJSON []byte
	if retryPolicy != nil {
		retryJSON, _ = json.Marshal(retryPolicy)
	}

	now := time.Now().UTC()
	jobIDs := make([]string, 0, len(deviceIDs))

	for _, deviceID := range deviceIDs {
		jobID := models.NewJobID()
		_, err := h.pool.Exec(ctx,
			`INSERT INTO jobs (id, tenant_id, device_id, type, status, payload, retry_policy, max_retries, created_by, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			jobID, tenantID, deviceID, req.Type, models.JobStatusQueued,
			req.Payload, retryJSON, maxRetries, userID, now,
		)
		if err != nil {
			h.log.Error("failed to create job",
				slog.String("device_id", deviceID), slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to create job")
			return
		}
		jobIDs = append(jobIDs, jobID)
	}

	// Audit log
	if h.audit != nil {
		_ = h.audit.LogAction(ctx, tenantID, userID, models.ActorTypeUser,
			"job.create", "job", jobIDs[0], map[string]string{
				"type":         req.Type,
				"device_count": slog.IntValue(len(deviceIDs)).String(),
			})
	}

	JSON(w, http.StatusCreated, CreateResponse{
		JobIDs:            jobIDs,
		TargetDeviceCount: len(deviceIDs),
	})
}

// resolveTargets expands group_ids, tag_ids, site_ids to device_ids within tenant.
func (h *JobsHandler) resolveTargets(ctx context.Context, tenantID string, target models.JobTarget) ([]string, error) {
	seen := make(map[string]bool)
	var deviceIDs []string

	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			deviceIDs = append(deviceIDs, id)
		}
	}

	// Direct device IDs
	for _, id := range target.DeviceIDs {
		add(id)
	}

	// Expand groups
	for _, groupID := range target.GroupIDs {
		rows, err := h.pool.Query(ctx,
			`SELECT dg.device_id FROM device_groups dg
			 JOIN devices d ON d.id = dg.device_id
			 WHERE dg.group_id = $1 AND d.tenant_id = $2`,
			groupID, tenantID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Expand tags
	for _, tagID := range target.TagIDs {
		rows, err := h.pool.Query(ctx,
			`SELECT dt.device_id FROM device_tags dt
			 JOIN devices d ON d.id = dt.device_id
			 WHERE dt.tag_id = $1 AND d.tenant_id = $2`,
			tagID, tenantID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// Expand sites
	for _, siteID := range target.SiteIDs {
		rows, err := h.pool.Query(ctx,
			`SELECT ds.device_id FROM device_sites ds
			 JOIN devices d ON d.id = ds.device_id
			 WHERE ds.site_id = $1 AND d.tenant_id = $2`,
			siteID, tenantID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return deviceIDs, nil
}

// Get handles GET /v1/jobs/{job_id}.
func (h *JobsHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	jobID := chi.URLParam(r, "job_id")
	ctx := r.Context()

	var j models.Job
	var retryJSON, parentJobID *string
	err := h.pool.QueryRow(ctx,
		`SELECT id, tenant_id, device_id, parent_job_id, type, status, payload,
				retry_policy, retry_count, max_retries, last_error, created_by,
				created_at, dispatched_at, acknowledged_at, started_at, completed_at
		 FROM jobs WHERE id = $1 AND tenant_id = $2`,
		jobID, tenantID,
	).Scan(&j.ID, &j.TenantID, &j.DeviceID, &parentJobID, &j.Type, &j.Status, &j.Payload,
		&retryJSON, &j.RetryCount, &j.MaxRetries, &j.LastError, &j.CreatedBy,
		&j.CreatedAt, &j.DispatchedAt, &j.AcknowledgedAt, &j.StartedAt, &j.CompletedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("failed to get job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.pool, tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, j.DeviceID) {
				Error(w, http.StatusNotFound, "job not found")
				return
			}
		}
	}

	if parentJobID != nil {
		j.ParentJobID = *parentJobID
	}
	if retryJSON != nil {
		var rp models.RetryPolicy
		if err := json.Unmarshal([]byte(*retryJSON), &rp); err == nil {
			j.RetryPolicy = &rp
		}
	}

	// Fetch result if terminal
	if models.IsTerminalStatus(j.Status) {
		var result models.JobResult
		err := h.pool.QueryRow(ctx,
			`SELECT id, job_id, exit_code, stdout, stderr, started_at, completed_at
			 FROM job_results WHERE job_id = $1`,
			j.ID,
		).Scan(&result.ID, &result.JobID, &result.ExitCode, &result.Stdout, &result.Stderr,
			&result.StartedAt, &result.CompletedAt)
		if err == nil {
			j.Result = &result
		}
	}

	JSON(w, http.StatusOK, j)
}

// List handles GET /v1/jobs.
func (h *JobsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	typeFilter := r.URL.Query().Get("type")
	deviceFilter := r.URL.Query().Get("device_id")

	// Scope enforcement — resolve allowed device IDs for scoped keys
	var scopeDeviceIDs []string
	if !auth.IsAdminFromContext(ctx) {
		scope := auth.ScopeFromContext(ctx)
		if scope != nil {
			allowed, err := auth.ResolveScope(ctx, h.pool, tenantID, scope)
			if err != nil {
				h.log.Error("failed to resolve scope", slog.String("error", err.Error()))
				Error(w, http.StatusInternalServerError, "failed to resolve scope")
				return
			}
			for id := range allowed {
				scopeDeviceIDs = append(scopeDeviceIDs, id)
			}
			if len(scopeDeviceIDs) == 0 {
				JSON(w, http.StatusOK, map[string]any{"data": []models.Job{}})
				return
			}
		}
	}

	query := `SELECT id, tenant_id, device_id, type, status, payload, created_at
			  FROM jobs WHERE tenant_id = $1`
	args := []any{tenantID}
	argIdx := 2

	if len(scopeDeviceIDs) > 0 {
		query += " AND device_id = ANY($" + strconv.Itoa(argIdx) + ")"
		args = append(args, scopeDeviceIDs)
		argIdx++
	}

	if statusFilter != "" {
		query += " AND status = $" + strconv.Itoa(argIdx)
		args = append(args, statusFilter)
		argIdx++
	}
	if typeFilter != "" {
		query += " AND type = $" + strconv.Itoa(argIdx)
		args = append(args, typeFilter)
		argIdx++
	}
	if deviceFilter != "" {
		query += " AND device_id = $" + strconv.Itoa(argIdx)
		args = append(args, deviceFilter)
		argIdx++
	}

	_ = argIdx // suppress unused warning
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := h.pool.Query(ctx, query, args...)
	if err != nil {
		h.log.Error("failed to list jobs", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	defer rows.Close()

	var result []models.Job
	for rows.Next() {
		var j models.Job
		if err := rows.Scan(&j.ID, &j.TenantID, &j.DeviceID, &j.Type, &j.Status, &j.Payload, &j.CreatedAt); err != nil {
			h.log.Error("failed to scan job", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to list jobs")
			return
		}
		result = append(result, j)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("failed to iterate jobs", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}

	if result == nil {
		result = []models.Job{}
	}

	JSON(w, http.StatusOK, map[string]any{"data": result})
}

// Cancel handles POST /v1/jobs/{job_id}/cancel.
func (h *JobsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	jobID := chi.URLParam(r, "job_id")
	ctx := r.Context()
	now := time.Now().UTC()

	var currentStatus, deviceID string
	err := h.pool.QueryRow(ctx,
		`SELECT status, device_id FROM jobs WHERE id = $1 AND tenant_id = $2`,
		jobID, tenantID,
	).Scan(&currentStatus, &deviceID)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("failed to get job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.pool, tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, deviceID) {
				ErrorWithCode(w, http.StatusForbidden, "scope_violation", "job target device is outside this key's scope")
				return
			}
		}
	}

	if !jobs.IsCancellable(currentStatus) {
		ErrorWithCode(w, http.StatusConflict, "not_cancellable",
			"job in state "+currentStatus+" cannot be cancelled")
		return
	}

	_, err = h.pool.Exec(ctx,
		`UPDATE jobs SET status = $1, completed_at = $2 WHERE id = $3`,
		models.JobStatusCancelled, now, jobID,
	)
	if err != nil {
		h.log.Error("failed to cancel job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to cancel job")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(ctx, tenantID, userID, models.ActorTypeUser,
			"job.cancel", "job", jobID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Retry handles POST /v1/jobs/{job_id}/retry.
func (h *JobsHandler) Retry(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	jobID := chi.URLParam(r, "job_id")
	ctx := r.Context()
	now := time.Now().UTC()

	var j models.Job
	var retryJSON, parentJobID, createdBy *string
	err := h.pool.QueryRow(ctx,
		`SELECT id, tenant_id, device_id, parent_job_id, type, status, payload,
				retry_policy, retry_count, max_retries, created_by
		 FROM jobs WHERE id = $1 AND tenant_id = $2`,
		jobID, tenantID,
	).Scan(&j.ID, &j.TenantID, &j.DeviceID, &parentJobID, &j.Type, &j.Status, &j.Payload,
		&retryJSON, &j.RetryCount, &j.MaxRetries, &createdBy)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("failed to get job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get job")
		return
	}

	// Scope enforcement — admin bypass
	if !auth.IsAdminFromContext(ctx) {
		if allowed, err := auth.ResolveScope(ctx, h.pool, tenantID, auth.ScopeFromContext(ctx)); err == nil {
			if !auth.DeviceInScope(allowed, j.DeviceID) {
				ErrorWithCode(w, http.StatusForbidden, "scope_violation", "job target device is outside this key's scope")
				return
			}
		}
	}

	if !jobs.IsRetryable(j.Status) {
		ErrorWithCode(w, http.StatusConflict, "not_retryable",
			"job in state "+j.Status+" cannot be retried")
		return
	}

	retryJobID := models.NewJobID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO jobs (id, tenant_id, device_id, parent_job_id, type, status, payload,
						   retry_policy, retry_count, max_retries, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		retryJobID, tenantID, j.DeviceID, jobID, j.Type, models.JobStatusQueued,
		j.Payload, retryJSON, j.RetryCount+1, j.MaxRetries, createdBy, now,
	)
	if err != nil {
		h.log.Error("failed to create retry job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create retry job")
		return
	}

	if h.audit != nil {
		_ = h.audit.LogAction(ctx, tenantID, userID, models.ActorTypeUser,
			"job.retry", "job", retryJobID, map[string]string{
				"parent_job_id": jobID,
			})
	}

	JSON(w, http.StatusCreated, map[string]string{"job_id": retryJobID})
}
