package api

import (
	"crypto/x509"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/health"
	"github.com/eavalenzuela/Moebius/server/pki"
	"github.com/eavalenzuela/Moebius/server/rbac"
	"github.com/eavalenzuela/Moebius/server/storage"
	"github.com/eavalenzuela/Moebius/server/store"
)

// RouterConfig holds the dependencies needed to build the API router.
type RouterConfig struct {
	Pool              *pgxpool.Pool
	Store             *store.Store
	CA                *pki.CA
	Audit             *audit.Logger
	Log               *slog.Logger
	Health            *health.Handler
	Enrollment        *auth.EnrollmentService
	Storage           storage.Backend
	TrustedProxyCIDRs string // comma-separated CIDRs; empty disables proxy cert header
}

// NewRouter creates the fully wired chi router for the API server.
func NewRouter(cfg RouterConfig) http.Handler {
	r := chi.NewRouter()

	// Build CA cert pool for mTLS chain verification (used in both direct
	// and passthrough modes — in direct mode Go's TLS layer also checks,
	// in passthrough mode the middleware verifies the forwarded cert).
	var caCertPool *x509.CertPool
	if cfg.CA != nil {
		caCertPool = x509.NewCertPool()
		caCertPool.AddCert(cfg.CA.Cert)
	}

	// Global middleware
	r.Use(RequestID)
	r.Use(MetricsMiddleware)

	// Strip X-Client-Cert from untrusted sources to prevent header spoofing.
	if cfg.TrustedProxyCIDRs != "" {
		sanitizer, err := auth.NewProxyCertSanitizer(cfg.TrustedProxyCIDRs)
		if err != nil {
			cfg.Log.Error("invalid TRUSTED_PROXY_CIDRS, disabling proxy cert header",
				slog.String("error", err.Error()))
		} else {
			r.Use(sanitizer.Handler)
		}
	}

	// Health & metrics (unauthenticated)
	r.Get("/health", cfg.Health.Liveness)
	r.Get("/health/ready", cfg.Health.Readiness)
	r.Handle("/metrics", promhttp.Handler())

	// Agent enrollment (unauthenticated, token-gated)
	enrollHandler := NewEnrollHandler(cfg.Pool, cfg.Enrollment, cfg.CA, cfg.Audit, cfg.Log)
	r.Post("/v1/agents/enroll", enrollHandler.ServeHTTP)

	// Install script (unauthenticated, enrollment-token-gated via query param)
	installScript := NewInstallScriptHandler(cfg.Pool, cfg.Enrollment, cfg.Audit, cfg.Log)
	r.Get("/v1/install/{os}/{arch}", installScript.ServeInstallScript)

	// Installer downloads (self-authenticated: accepts API key or enrollment token)
	installersDownload := NewInstallersHandler(cfg.Pool, cfg.Storage, cfg.Enrollment, cfg.Audit, cfg.Log)
	r.Get("/v1/installers/{os}/{arch}/latest", installersDownload.DownloadLatest)
	r.Get("/v1/installers/{os}/{arch}/{version}", installersDownload.Download)
	r.Get("/v1/installers/{os}/{arch}/{version}/checksum", installersDownload.Checksum)
	r.Get("/v1/installers/{os}/{arch}/{version}/signature", installersDownload.Signature)

	// Agent endpoints (mTLS-authenticated)
	mtls := auth.NewMTLSMiddleware(cfg.Pool, cfg.Log, caCertPool)
	r.Route("/v1/agents", func(r chi.Router) {
		r.Use(mtls.Handler)
		renewHandler := NewRenewHandler(cfg.Pool, cfg.CA, cfg.Audit, cfg.Log)
		r.Post("/renew", renewHandler.ServeHTTP)

		checkinHandler := NewCheckinHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.Post("/checkin", checkinHandler.ServeHTTP)

		agentJobs := NewAgentJobsHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.Post("/jobs/{job_id}/acknowledge", agentJobs.Acknowledge)
		r.Post("/jobs/{job_id}/result", agentJobs.SubmitResult)

		logsHandler := NewLogsHandler(cfg.Pool, cfg.Log)
		r.Post("/logs", logsHandler.Ingest)

		filesH := NewFilesHandler(cfg.Pool, cfg.Storage, cfg.Audit, cfg.Log)
		r.Get("/files/{file_id}/download", filesH.Download)

		// Agent-facing signing key fetch (for update signature verification)
		agentSigKeys := NewAgentSigningKeysHandler(cfg.Pool, cfg.Log)
		r.Get("/signing-keys/{key_id}", agentSigKeys.Get)
	})

	// File data serving (local backend) — no auth (URL from download endpoint)
	{
		localFilesH := NewFilesHandler(cfg.Pool, cfg.Storage, cfg.Audit, cfg.Log)
		r.Get("/v1/files/data/{file_id}", localFilesH.ServeFileData)
	}

	// API endpoints (API key / OIDC authenticated)
	apiKeyAuth := auth.NewAPIKeyMiddleware(cfg.Pool, cfg.Log)
	r.Route("/v1", func(r chi.Router) {
		r.Use(apiKeyAuth.Handler)
		r.Use(auth.RequireTenant)

		// Roles
		roles := NewRolesHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermRolesRead)).Get("/roles", roles.List)
		r.With(rbac.Require(rbac.PermRolesWrite)).Post("/roles", roles.Create)
		r.With(rbac.Require(rbac.PermRolesRead)).Get("/roles/{role_id}", roles.Get)
		r.With(rbac.Require(rbac.PermRolesWrite)).Patch("/roles/{role_id}", roles.Update)
		r.With(rbac.Require(rbac.PermRolesWrite)).Delete("/roles/{role_id}", roles.Delete)

		// Users
		users := NewUsersHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermUsersRead)).Get("/users", users.List)
		r.With(rbac.Require(rbac.PermUsersRead)).Get("/users/{user_id}", users.Get)
		r.With(rbac.Require(rbac.PermUsersWrite)).Post("/users/invite", users.Invite)
		r.With(rbac.Require(rbac.PermUsersWrite)).Patch("/users/{user_id}", users.Update)
		r.With(rbac.Require(rbac.PermUsersWrite)).Post("/users/{user_id}/deactivate", users.Deactivate)

		// API Keys
		apiKeys := NewAPIKeysHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermAPIKeysRead)).Get("/api-keys", apiKeys.List)
		r.With(rbac.Require(rbac.PermAPIKeysWrite)).Post("/api-keys", apiKeys.Create)
		r.With(rbac.Require(rbac.PermAPIKeysWrite)).Delete("/api-keys/{key_id}", apiKeys.Delete)

		// Tenant
		tenant := NewTenantHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermTenantRead)).Get("/tenant", tenant.Get)
		r.With(rbac.Require(rbac.PermTenantWrite)).Patch("/tenant", tenant.Update)

		// Jobs
		jobsH := NewJobsHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermJobsRead)).Get("/jobs", jobsH.List)
		r.With(rbac.Require(rbac.PermJobsCreate)).Post("/jobs", jobsH.Create)
		r.With(rbac.Require(rbac.PermJobsRead)).Get("/jobs/{job_id}", jobsH.Get)
		r.With(rbac.Require(rbac.PermJobsCreate)).Post("/jobs/{job_id}/cancel", jobsH.Cancel)
		r.With(rbac.Require(rbac.PermJobsRetry)).Post("/jobs/{job_id}/retry", jobsH.Retry)

		// Devices
		devices := NewDevicesHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermDevicesRead)).Get("/devices", devices.List)
		r.With(rbac.Require(rbac.PermDevicesRead)).Get("/devices/{device_id}", devices.Get)
		r.With(rbac.Require(rbac.PermDevicesWrite)).Patch("/devices/{device_id}", devices.Update)
		r.With(rbac.Require(rbac.PermDevicesRevoke)).Post("/devices/{device_id}/revoke", devices.Revoke)

		// Device inventory
		inv := NewInventoryHandler(cfg.Pool)
		r.With(rbac.Require(rbac.PermInventoryRead)).Get("/devices/{device_id}/inventory", inv.GetDeviceInventory)

		// Device tags
		tags := NewTagsHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermTagsRead)).Get("/tags", tags.List)
		r.With(rbac.Require(rbac.PermTagsWrite)).Post("/tags", tags.Create)
		r.With(rbac.Require(rbac.PermTagsWrite)).Delete("/tags/{tag_id}", tags.Delete)
		r.With(rbac.Require(rbac.PermTagsWrite)).Post("/devices/{device_id}/tags", tags.AddToDevice)
		r.With(rbac.Require(rbac.PermTagsWrite)).Delete("/devices/{device_id}/tags/{tag_id}", tags.RemoveFromDevice)

		// Groups
		groups := NewGroupsHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermGroupsRead)).Get("/groups", groups.List)
		r.With(rbac.Require(rbac.PermGroupsWrite)).Post("/groups", groups.Create)
		r.With(rbac.Require(rbac.PermGroupsRead)).Get("/groups/{group_id}", groups.Get)
		r.With(rbac.Require(rbac.PermGroupsWrite)).Patch("/groups/{group_id}", groups.Update)
		r.With(rbac.Require(rbac.PermGroupsWrite)).Delete("/groups/{group_id}", groups.Delete)
		r.With(rbac.Require(rbac.PermGroupsRead)).Get("/groups/{group_id}/devices", groups.ListDevices)
		r.With(rbac.Require(rbac.PermGroupsWrite)).Post("/groups/{group_id}/devices", groups.AddDevices)
		r.With(rbac.Require(rbac.PermGroupsWrite)).Delete("/groups/{group_id}/devices/{device_id}", groups.RemoveDevice)

		// Sites
		sites := NewSitesHandler(cfg.Store, cfg.Audit)
		r.With(rbac.Require(rbac.PermSitesRead)).Get("/sites", sites.List)
		r.With(rbac.Require(rbac.PermSitesWrite)).Post("/sites", sites.Create)
		r.With(rbac.Require(rbac.PermSitesRead)).Get("/sites/{site_id}", sites.Get)
		r.With(rbac.Require(rbac.PermSitesWrite)).Patch("/sites/{site_id}", sites.Update)
		r.With(rbac.Require(rbac.PermSitesWrite)).Delete("/sites/{site_id}", sites.Delete)
		r.With(rbac.Require(rbac.PermSitesRead)).Get("/sites/{site_id}/devices", sites.ListDevices)
		r.With(rbac.Require(rbac.PermSitesWrite)).Post("/sites/{site_id}/devices", sites.AddDevices)
		r.With(rbac.Require(rbac.PermSitesWrite)).Delete("/sites/{site_id}/devices/{device_id}", sites.RemoveDevice)

		// Signing keys
		sigKeys := NewSigningKeysHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermSigningKeysRead)).Get("/signing-keys", sigKeys.List)
		r.With(rbac.Require(rbac.PermSigningKeysWrite)).Post("/signing-keys", sigKeys.Create)
		r.With(rbac.Require(rbac.PermSigningKeysWrite)).Delete("/signing-keys/{key_id}", sigKeys.Delete)

		// Files
		filesAPI := NewFilesHandler(cfg.Pool, cfg.Storage, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermFilesRead)).Get("/files", filesAPI.ListFiles)
		r.With(rbac.Require(rbac.PermFilesWrite)).Post("/files", filesAPI.InitiateUpload)
		r.With(rbac.Require(rbac.PermFilesRead)).Get("/files/{file_id}", filesAPI.GetFile)
		r.With(rbac.Require(rbac.PermFilesWrite)).Delete("/files/{file_id}", filesAPI.DeleteFile)
		r.With(rbac.Require(rbac.PermFilesWrite)).Put("/files/uploads/{upload_id}/chunks/{chunk_index}", filesAPI.UploadChunk)
		r.With(rbac.Require(rbac.PermFilesRead)).Get("/files/uploads/{upload_id}", filesAPI.UploadStatus)
		r.With(rbac.Require(rbac.PermFilesWrite)).Post("/files/uploads/{upload_id}/complete", filesAPI.CompleteUpload)

		// Agent versions
		agentVer := NewAgentVersionsHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermAgentVersionsRead)).Get("/agent-versions", agentVer.List)
		r.With(rbac.Require(rbac.PermAgentVersionsWrite)).Post("/agent-versions", agentVer.Create)
		r.With(rbac.Require(rbac.PermAgentVersionsRead)).Get("/agent-versions/{version}", agentVer.Get)
		r.With(rbac.Require(rbac.PermAgentVersionsWrite)).Post("/agent-versions/{version}/yank", agentVer.Yank)

		// Rollout management
		rollouts := NewRolloutsHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermAgentVersionsRead)).Get("/agent-versions/{version}/rollout", rollouts.GetStatus)
		r.With(rbac.Require(rbac.PermAgentVersionsWrite)).Post("/agent-versions/{version}/rollout/pause", rollouts.Pause)
		r.With(rbac.Require(rbac.PermAgentVersionsWrite)).Post("/agent-versions/{version}/rollout/resume", rollouts.Resume)
		r.With(rbac.Require(rbac.PermAgentVersionsWrite)).Post("/agent-versions/{version}/rollout/abort", rollouts.Abort)

		// Update policies
		upPolicies := NewUpdatePoliciesHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermUpdatePoliciesRead)).Get("/update-policies", upPolicies.List)
		r.With(rbac.Require(rbac.PermUpdatePoliciesWrite)).Post("/update-policies", upPolicies.Upsert)
		r.With(rbac.Require(rbac.PermUpdatePoliciesWrite)).Delete("/update-policies/{policy_id}", upPolicies.Delete)

		// Device rollback
		devRollback := NewDeviceRollbackHandler(cfg.Pool, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermDevicesWrite)).Post("/devices/{device_id}/rollback", devRollback.Rollback)

		// Scheduled jobs
		schedJobs := NewScheduledJobsHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermScheduledJobsRead)).Get("/scheduled-jobs", schedJobs.List)
		r.With(rbac.Require(rbac.PermScheduledJobsWrite)).Post("/scheduled-jobs", schedJobs.Create)
		r.With(rbac.Require(rbac.PermScheduledJobsRead)).Get("/scheduled-jobs/{scheduled_job_id}", schedJobs.Get)
		r.With(rbac.Require(rbac.PermScheduledJobsWrite)).Patch("/scheduled-jobs/{scheduled_job_id}", schedJobs.Update)
		r.With(rbac.Require(rbac.PermScheduledJobsWrite)).Delete("/scheduled-jobs/{scheduled_job_id}", schedJobs.Delete)
		r.With(rbac.Require(rbac.PermScheduledJobsWrite)).Post("/scheduled-jobs/{scheduled_job_id}/enable", schedJobs.Enable)
		r.With(rbac.Require(rbac.PermScheduledJobsWrite)).Post("/scheduled-jobs/{scheduled_job_id}/disable", schedJobs.Disable)

		// Alert rules
		alertRules := NewAlertRulesHandler(cfg.Store, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermAlertsRead)).Get("/alert-rules", alertRules.List)
		r.With(rbac.Require(rbac.PermAlertsWrite)).Post("/alert-rules", alertRules.Create)
		r.With(rbac.Require(rbac.PermAlertsRead)).Get("/alert-rules/{rule_id}", alertRules.Get)
		r.With(rbac.Require(rbac.PermAlertsWrite)).Patch("/alert-rules/{rule_id}", alertRules.Update)
		r.With(rbac.Require(rbac.PermAlertsWrite)).Delete("/alert-rules/{rule_id}", alertRules.Delete)
		r.With(rbac.Require(rbac.PermAlertsWrite)).Post("/alert-rules/{rule_id}/enable", alertRules.Enable)
		r.With(rbac.Require(rbac.PermAlertsWrite)).Post("/alert-rules/{rule_id}/disable", alertRules.Disable)

		// Audit log
		auditLogH := NewAuditLogHandler(cfg.Pool, cfg.Log)
		r.With(rbac.Require(rbac.PermAuditLogRead)).Get("/audit-log", auditLogH.List)

		// Enrollment tokens
		enrollTokens := NewEnrollmentTokensHandler(cfg.Pool, cfg.Enrollment, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermEnrollmentTokenWrite)).Get("/enrollment-tokens", enrollTokens.List)
		r.With(rbac.Require(rbac.PermEnrollmentTokenWrite)).Post("/enrollment-tokens", enrollTokens.Create)
		r.With(rbac.Require(rbac.PermEnrollmentTokenWrite)).Delete("/enrollment-tokens/{token_id}", enrollTokens.Delete)

		// Install command generation (requires enrollment token write permission)
		installCmd := NewInstallScriptHandler(cfg.Pool, cfg.Enrollment, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermEnrollmentTokenWrite)).Post("/enrollment-tokens/{token_id}/install-command", installCmd.GenerateInstallCommand)

		// Installers (list + admin upload — API key auth)
		installersAPI := NewInstallersHandler(cfg.Pool, cfg.Storage, cfg.Enrollment, cfg.Audit, cfg.Log)
		r.With(rbac.Require(rbac.PermInstallersRead)).Get("/installers", installersAPI.List)
		r.With(rbac.Require(rbac.PermInstallersWrite)).Post("/installers", installersAPI.Create)
	})

	return r
}
