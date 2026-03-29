package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/moebius-oss/moebius/server/audit"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/server/health"
	"github.com/moebius-oss/moebius/server/pki"
	"github.com/moebius-oss/moebius/server/rbac"
	"github.com/moebius-oss/moebius/server/store"
)

// RouterConfig holds the dependencies needed to build the API router.
type RouterConfig struct {
	Pool       *pgxpool.Pool
	Store      *store.Store
	CA         *pki.CA
	Audit      *audit.Logger
	Log        *slog.Logger
	Health     *health.Handler
	Enrollment *auth.EnrollmentService
}

// NewRouter creates the fully wired chi router for the API server.
func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(RequestID)
	r.Use(MetricsMiddleware)

	// Health & metrics (unauthenticated)
	r.Get("/health", cfg.Health.Liveness)
	r.Get("/health/ready", cfg.Health.Readiness)
	r.Handle("/metrics", promhttp.Handler())

	// Agent enrollment (unauthenticated, token-gated)
	enrollHandler := NewEnrollHandler(cfg.Pool, cfg.Enrollment, cfg.CA, cfg.Audit, cfg.Log)
	r.Post("/v1/agents/enroll", enrollHandler.ServeHTTP)

	// Agent endpoints (mTLS-authenticated)
	mtls := auth.NewMTLSMiddleware(cfg.Pool, cfg.Log)
	r.Route("/v1/agents", func(r chi.Router) {
		r.Use(mtls.Handler)
		renewHandler := NewRenewHandler(cfg.Pool, cfg.CA, cfg.Audit, cfg.Log)
		r.Post("/renew", renewHandler.ServeHTTP)

		checkinHandler := NewCheckinHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.Post("/checkin", checkinHandler.ServeHTTP)

		agentJobs := NewAgentJobsHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.Post("/jobs/{job_id}/acknowledge", agentJobs.Acknowledge)
		r.Post("/jobs/{job_id}/result", agentJobs.SubmitResult)
	})

	// API endpoints (API key / OIDC authenticated)
	apiKeyAuth := auth.NewAPIKeyMiddleware(cfg.Pool, cfg.Log)
	r.Route("/v1", func(r chi.Router) {
		r.Use(apiKeyAuth.Handler)
		r.Use(auth.RequireTenant)

		// Roles
		roles := NewRolesHandler(cfg.Store)
		r.With(rbac.Require(rbac.PermRolesRead)).Get("/roles", roles.List)
		r.With(rbac.Require(rbac.PermRolesWrite)).Post("/roles", roles.Create)
		r.With(rbac.Require(rbac.PermRolesRead)).Get("/roles/{role_id}", roles.Get)
		r.With(rbac.Require(rbac.PermRolesWrite)).Patch("/roles/{role_id}", roles.Update)
		r.With(rbac.Require(rbac.PermRolesWrite)).Delete("/roles/{role_id}", roles.Delete)

		// Users
		users := NewUsersHandler(cfg.Store)
		r.With(rbac.Require(rbac.PermUsersRead)).Get("/users", users.List)
		r.With(rbac.Require(rbac.PermUsersRead)).Get("/users/{user_id}", users.Get)
		r.With(rbac.Require(rbac.PermUsersWrite)).Post("/users/invite", users.Invite)
		r.With(rbac.Require(rbac.PermUsersWrite)).Patch("/users/{user_id}", users.Update)
		r.With(rbac.Require(rbac.PermUsersWrite)).Post("/users/{user_id}/deactivate", users.Deactivate)

		// API Keys
		apiKeys := NewAPIKeysHandler(cfg.Store)
		r.With(rbac.Require(rbac.PermAPIKeysRead)).Get("/api-keys", apiKeys.List)
		r.With(rbac.Require(rbac.PermAPIKeysWrite)).Post("/api-keys", apiKeys.Create)
		r.With(rbac.Require(rbac.PermAPIKeysWrite)).Delete("/api-keys/{key_id}", apiKeys.Delete)

		// Tenant
		tenant := NewTenantHandler(cfg.Store)
		r.With(rbac.Require(rbac.PermTenantRead)).Get("/tenant", tenant.Get)
		r.With(rbac.Require(rbac.PermTenantWrite)).Patch("/tenant", tenant.Update)

		// Jobs
		jobsH := NewJobsHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermJobsRead)).Get("/jobs", jobsH.List)
		r.With(rbac.Require(rbac.PermJobsCreate)).Post("/jobs", jobsH.Create)
		r.With(rbac.Require(rbac.PermJobsRead)).Get("/jobs/{job_id}", jobsH.Get)
		r.With(rbac.Require(rbac.PermJobsCreate)).Post("/jobs/{job_id}/cancel", jobsH.Cancel)
		r.With(rbac.Require(rbac.PermJobsRetry)).Post("/jobs/{job_id}/retry", jobsH.Retry)

		// Device revocation
		devices := NewDevicesHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermDevicesRevoke)).Post("/devices/{device_id}/revoke", devices.Revoke)
	})

	return r
}
