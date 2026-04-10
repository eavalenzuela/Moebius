package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/jobs"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// ScheduledJobsHandler handles /v1/scheduled-jobs endpoints.
type ScheduledJobsHandler struct {
	store *store.Store
	audit *audit.Logger
	log   *slog.Logger
}

// NewScheduledJobsHandler creates a ScheduledJobsHandler.
func NewScheduledJobsHandler(st *store.Store, auditLog *audit.Logger, log *slog.Logger) *ScheduledJobsHandler {
	return &ScheduledJobsHandler{store: st, audit: auditLog, log: log}
}

type createScheduledJobRequest struct {
	Name        string              `json:"name"`
	JobType     string              `json:"job_type"`
	Payload     json.RawMessage     `json:"payload"`
	Target      models.JobTarget    `json:"target"`
	CronExpr    string              `json:"cron_expr"`
	RetryPolicy *models.RetryPolicy `json:"retry_policy,omitempty"`
	Enabled     *bool               `json:"enabled,omitempty"`
}

// Create handles POST /v1/scheduled-jobs.
func (h *ScheduledJobsHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())

	var req createScheduledJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := jobs.ValidateType(req.JobType); err != nil {
		ErrorWithCode(w, http.StatusBadRequest, "invalid_job_type", err.Error())
		return
	}
	if len(req.Payload) == 0 {
		Error(w, http.StatusBadRequest, "payload is required")
		return
	}

	// Scope enforcement: validate target overlaps with API key scope
	if !auth.IsAdminFromContext(r.Context()) {
		scope := auth.ScopeFromContext(r.Context())
		if scope != nil && !auth.TargetOverlapsScope(scope, &req.Target) {
			ErrorWithCode(w, http.StatusForbidden, "scope_violation", "scheduled job target is outside this key's scope")
			return
		}
	}

	// Validate cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(req.CronExpr)
	if err != nil {
		ErrorWithCode(w, http.StatusBadRequest, "invalid_cron", "invalid cron expression: "+err.Error())
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	now := time.Now().UTC()
	nextRun := sched.Next(now)

	sj := &models.ScheduledJob{
		ID:          models.NewScheduledJobID(),
		TenantID:    tenantID,
		Name:        req.Name,
		JobType:     req.JobType,
		Payload:     req.Payload,
		Target:      &req.Target,
		CronExpr:    req.CronExpr,
		RetryPolicy: req.RetryPolicy,
		Enabled:     enabled,
		NextRunAt:   &nextRun,
	}

	if err := h.store.CreateScheduledJob(r.Context(), sj); err != nil {
		h.log.Error("failed to create scheduled job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to create scheduled job")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"scheduled_job.create", "scheduled_job", sj.ID, nil)
	}

	JSON(w, http.StatusCreated, sj)
}

// List handles GET /v1/scheduled-jobs.
func (h *ScheduledJobsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())

	result, err := h.store.ListScheduledJobs(r.Context(), tenantID)
	if err != nil {
		h.log.Error("failed to list scheduled jobs", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to list scheduled jobs")
		return
	}
	if result == nil {
		result = []models.ScheduledJob{}
	}
	JSON(w, http.StatusOK, map[string]any{"data": result})
}

// Get handles GET /v1/scheduled-jobs/{scheduled_job_id}.
func (h *ScheduledJobsHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	id := chi.URLParam(r, "scheduled_job_id")

	sj, err := h.store.GetScheduledJob(r.Context(), tenantID, id)
	if err != nil {
		h.log.Error("failed to get scheduled job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to get scheduled job")
		return
	}
	if sj == nil {
		Error(w, http.StatusNotFound, "scheduled job not found")
		return
	}
	JSON(w, http.StatusOK, sj)
}

type updateScheduledJobRequest struct {
	Name        *string             `json:"name,omitempty"`
	JobType     *string             `json:"job_type,omitempty"`
	Payload     json.RawMessage     `json:"payload,omitempty"`
	Target      *models.JobTarget   `json:"target,omitempty"`
	CronExpr    *string             `json:"cron_expr,omitempty"`
	RetryPolicy *models.RetryPolicy `json:"retry_policy,omitempty"`
}

// Update handles PATCH /v1/scheduled-jobs/{scheduled_job_id}.
func (h *ScheduledJobsHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "scheduled_job_id")

	var req updateScheduledJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := make(map[string]any)
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.JobType != nil {
		if err := jobs.ValidateType(*req.JobType); err != nil {
			ErrorWithCode(w, http.StatusBadRequest, "invalid_job_type", err.Error())
			return
		}
		updates["job_type"] = *req.JobType
	}
	if req.Payload != nil {
		updates["payload"] = req.Payload
	}
	if req.Target != nil {
		targetJSON, _ := json.Marshal(req.Target)
		updates["target"] = targetJSON
	}
	if req.CronExpr != nil {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(*req.CronExpr)
		if err != nil {
			ErrorWithCode(w, http.StatusBadRequest, "invalid_cron", "invalid cron expression: "+err.Error())
			return
		}
		updates["cron_expr"] = *req.CronExpr
		nextRun := sched.Next(time.Now().UTC())
		updates["next_run_at"] = nextRun
	}
	if req.RetryPolicy != nil {
		retryJSON, _ := json.Marshal(req.RetryPolicy)
		updates["retry_policy"] = retryJSON
	}

	if len(updates) == 0 {
		Error(w, http.StatusBadRequest, "no fields to update")
		return
	}

	if err := h.store.UpdateScheduledJob(r.Context(), tenantID, id, updates); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "scheduled job not found")
			return
		}
		h.log.Error("failed to update scheduled job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to update scheduled job")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"scheduled_job.update", "scheduled_job", id, nil)
	}

	// Return updated object
	sj, _ := h.store.GetScheduledJob(r.Context(), tenantID, id)
	JSON(w, http.StatusOK, sj)
}

// Delete handles DELETE /v1/scheduled-jobs/{scheduled_job_id}.
func (h *ScheduledJobsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "scheduled_job_id")

	if err := h.store.DeleteScheduledJob(r.Context(), tenantID, id); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "scheduled job not found")
			return
		}
		h.log.Error("failed to delete scheduled job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to delete scheduled job")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"scheduled_job.delete", "scheduled_job", id, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Enable handles POST /v1/scheduled-jobs/{scheduled_job_id}/enable.
func (h *ScheduledJobsHandler) Enable(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "scheduled_job_id")

	if err := h.store.SetScheduledJobEnabled(r.Context(), tenantID, id, true); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "scheduled job not found")
			return
		}
		h.log.Error("failed to enable scheduled job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to enable scheduled job")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"scheduled_job.enable", "scheduled_job", id, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Disable handles POST /v1/scheduled-jobs/{scheduled_job_id}/disable.
func (h *ScheduledJobsHandler) Disable(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	id := chi.URLParam(r, "scheduled_job_id")

	if err := h.store.SetScheduledJobEnabled(r.Context(), tenantID, id, false); err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "scheduled job not found")
			return
		}
		h.log.Error("failed to disable scheduled job", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to disable scheduled job")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, userID, models.ActorTypeUser,
			"scheduled_job.disable", "scheduled_job", id, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}
