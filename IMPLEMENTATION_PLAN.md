# Implementation Plan

This plan is ordered by dependency. Each phase builds on the prior one. Tasks within a phase can generally be parallelized.

---

## Phase 0 — Project Scaffolding

- [x] **0.1** Initialize Go module and directory structure
- `go mod init` at repo root
- Create the monorepo directory tree per `HIGH-LEVEL_DESIGN.md`:
  ```
  agent/cmd/agent/        server/cmd/api/       shared/models/
  agent/poller/           server/cmd/worker/    shared/protocol/
  agent/executor/         server/cmd/scheduler/ shared/version/
  agent/inventory/        server/api/
  agent/cdm/             server/auth/
  agent/localui/         server/rbac/
  agent/localcli/        server/jobs/
  agent/platform/linux/  server/worker/
  agent/platform/windows/ server/store/
  agent/update/          server/notify/
  ui/                    cli/
  deploy/docker-compose.yml
  deploy/helm/
  deploy/migrations/
  keys/
  ```
- Add `.gitignore` for Go, Node, dist/, *.env

- [x] **0.2** Build system (Makefile)
- Targets: `build-api`, `build-worker`, `build-scheduler`, `build-agent` (per GOOS/GOARCH matrix from `INSTALLER_PACKAGING_SPEC.md`)
- `lint` (golangci-lint), `test`, `test-integration`
- `migrate` (run migrations), `generate` (code generation if any)
- `docker-build` for all three server images
- `dist` for agent tarballs/MSI

- [x] **0.3** CI/CD skeleton (GitHub Actions)
- Workflow: lint + test on PR
- Workflow: release on tag push (`v*`) — build matrix, sign, publish (per `INSTALLER_PACKAGING_SPEC.md` release pipeline)
- Placeholder Dockerfiles for api, worker, scheduler (distroless or alpine base per `SERVER_DEPLOYMENT_SPEC.md`)

- [x] **0.4** Shared version package
- `shared/version/version.go` — embed build version via `-ldflags`
- Used by all binaries for `GET /v1/health`, `GET /v1/version`, agent check-in `agent_version` field

---

## Phase 1 — Schema & Shared Types

- [x] **1.1** Reference schema (`deploy/schema.sql`)
Single development-time schema file containing all tables. Not a migration — just a reference used to create/reset dev databases. Will be frozen into a proper migration in Phase 17 before first deployment.
Tables from all specs:
- Core: `tenants`, `users`, `roles`, `api_keys`, `devices`
- Grouping: `groups`, `device_groups`, `tags`, `device_tags`, `sites`, `device_sites`
- Inventory: `inventory_hardware`, `inventory_packages`
- Jobs: `jobs`, `job_results`, `scheduled_jobs`
- Audit: `audit_log`
- Alerts: `alert_rules`
- Auth: `agent_certificates`, `enrollment_tokens`
- Files: `files`, `file_uploads`, `signing_keys`
- Updates: `agent_versions`, `agent_version_binaries`, `agent_update_policies`
- Installers: `installers`

- [x] **1.2** Shared domain models (`shared/models/`)
Go structs mirroring core DB types: `Tenant`, `User`, `Role`, `Device`, `Job`, `JobResult`, `AuditEntry`, `Group`, `Tag`, `Site`, etc.
- Use struct tags for JSON serialization matching the API spec field names
- ID types using prefixed UUIDs (`agt_`, `dev_`, `job_`, `ten_`, etc.)

- [x] **1.3** Shared protocol types (`shared/protocol/`)
Request/response types for agent<->server communication per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md`:
- `CheckinRequest` / `CheckinResponse`
- `EnrollRequest` / `EnrollResponse`
- `RenewRequest` / `RenewResponse`
- `JobResultSubmission`
- `LogShipment`
- Job payload types per job type: `ExecPayload`, `PackageInstallPayload`, `FileTransferPayload`, `AgentUpdatePayload`, `InventoryFullPayload`

---

## Phase 2 — Server Core

- [ ] **2.1** Configuration loading
- Environment variable parsing per `SERVER_DEPLOYMENT_SPEC.md` env var reference
- Shared config struct used by api, worker, and scheduler
- Validate required vars at startup, fail fast with clear errors

- [ ] **2.2** PostgreSQL connection pool (`server/store/`)
- Connection pool using `pgxpool`
- `store.Store` interface with methods per resource (devices, jobs, users, etc.)
- Tenant-scoped query helpers — every query filters by `tenant_id`
- Transaction support for multi-table operations

- [ ] **2.3** NATS JetStream client
- Connect to NATS, create/verify streams and consumers per `SERVER_DEPLOYMENT_SPEC.md`:
  - `jobs` stream (work queue, delete on ack)
  - `results` stream (interest-based)
  - `logs` stream (max age 7 days)
- Publish/subscribe helpers with subject patterns: `jobs.dispatch.{tenant_id}.{device_id}`, etc.

- [ ] **2.4** Structured logging
- JSON structured logger (zerolog or slog)
- Fields: `timestamp`, `level`, `service`, `version`, `request_id`, `tenant_id`
- Configurable via `LOG_LEVEL` and `LOG_FORMAT` env vars

- [ ] **2.5** Prometheus metrics endpoint
- `GET /metrics` on all three processes (not proxied externally)
- Register key metrics from `SERVER_DEPLOYMENT_SPEC.md`: `agent_checkins_total`, `agent_online`, `jobs_enqueued_total`, `jobs_completed_total`, `job_duration_seconds`, `job_queue_depth`, `file_transfer_bytes_total`, `api_request_duration_seconds`, `db_query_duration_seconds`

- [ ] **2.6** Health endpoints
- `GET /health` — liveness (process running)
- `GET /health/ready` — readiness (DB + NATS connections healthy)
- Used by Docker healthchecks and Kubernetes probes

- [ ] **2.7** Audit log service
- `server/store/audit.go` — append-only writes to `audit_log` table
- Called from all mutation paths (job create, device revoke, enrollment, etc.)
- Actor type resolution: `user`, `api_key`, `agent`, `system`

---

## Phase 3 — PKI & Agent Enrollment

- [ ] **3.1** Internal CA management
- Root CA + Intermediate CA generation (`agent-server-api generate-ca` command per `SERVER_DEPLOYMENT_SPEC.md` startup sequence)
- CSR signing logic — sign agent CSRs with intermediate CA
- Certificate serial number tracking
- CA cert/key loaded from `CA_CERT_PATH` / `CA_KEY_PATH` env vars

- [ ] **3.2** Enrollment token service
- Create tokens: single-use, time-limited (default 24h), optionally scoped to tenant/group/site/tag
- Store hashed (`token_hash`), never recoverable after issuance
- Validate on enrollment: check hash, check expiry, check not used, mark used atomically
- Audit-log on create, use, and expiry

- [ ] **3.3** Agent enrollment endpoint
`POST /v1/agents/enroll` (unauthenticated, token-gated per `AGENT_AUTH_SPEC.md`):
- Validate enrollment token
- Validate CSR
- Sign CSR with intermediate CA, set 90-day expiry, embed `agent_id` in SAN
- Create device record in DB, inherit scope from token
- Store certificate record in `agent_certificates`
- Return signed cert + CA chain + `agent_id` + `poll_interval_seconds`
- Audit-log

- [ ] **3.4** mTLS middleware for agent endpoints
- TLS config that requests and verifies client certificates against the intermediate CA
- Extract `agent_id` from certificate SAN
- Check certificate not expired, not revoked (CRL check against `agent_certificates.revoked_at`)
- Check `agent_id` maps to an active device record
- Attach device identity to request context

- [ ] **3.5** Certificate renewal endpoint
`POST /v1/agents/renew` (mTLS, per `AGENT_AUTH_SPEC.md`):
- Validate current cert is trusted and not revoked
- Sign new CSR, issue new cert
- Store new cert record; old cert remains valid until its natural expiry
- Audit-log

- [ ] **3.6** Device revocation
`POST /v1/devices/{device_id}/revoke` (per `REST_API_SPEC.md`):
- Mark certificate `revoked_at` in `agent_certificates`
- Mark device record as `revoked`
- Cancel all pending jobs for the device
- Audit-log

---

## Phase 4 — API Server: Auth & RBAC

- [ ] **4.1** API key authentication middleware
- `Authorization: Bearer <api_key>` header parsing
- Hash incoming key, look up in `api_keys` table
- Check not expired, update `last_used_at`
- Attach user identity + role + scope to request context

- [ ] **4.2** RBAC enforcement middleware (`server/rbac/`)
- Load role permissions from `roles` table
- Check required permission against role's permission set for every endpoint
- Scope enforcement: filter results by API key's `scope` (group_ids, tag_ids, site_ids, device_ids)
- Predefined roles per `FEATURE_REQUIREMENTS.md`: Super Admin, Tenant Admin, Operator, Technician, Viewer
- Permission strings per `REST_API_SPEC.md` permission reference (e.g. `devices:read`, `jobs:create`)

- [ ] **4.3** Tenant isolation middleware
- Extract `tenant_id` from authenticated user/API key
- Inject into request context; all store queries must use it
- Reject cross-tenant access attempts

- [ ] **4.4** Request/response framework
- Router setup (chi or standard library mux)
- Cursor-based pagination helper per `REST_API_SPEC.md` conventions (opaque cursor, default limit 50, max 500)
- Standard error response format: `{ "error": { "code", "message", "request_id" } }`
- `X-Request-ID` generation/echo
- Rate limit headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`

- [ ] **4.5** Roles CRUD endpoints
Per `REST_API_SPEC.md`:
- `GET /v1/roles` — list system + tenant custom roles
- `POST /v1/roles` — create custom role with permission list
- `GET /v1/roles/{role_id}`
- `PATCH /v1/roles/{role_id}` — system roles immutable
- `DELETE /v1/roles/{role_id}` — fails if assigned to users/keys

- [ ] **4.6** Users CRUD endpoints
- `GET /v1/users`, `GET /v1/users/{user_id}`
- `POST /v1/users/invite` — send invite with role assignment
- `PATCH /v1/users/{user_id}` — update role
- `POST /v1/users/{user_id}/deactivate`

- [ ] **4.7** API keys CRUD endpoints
- `GET /v1/api-keys` — list (no key values)
- `POST /v1/api-keys` — create, return key value once only
- `DELETE /v1/api-keys/{key_id}` — revoke

- [ ] **4.8** Tenant endpoints
- `GET /v1/tenant` — current tenant config
- `PATCH /v1/tenant` — update name, config (poll interval, cert lifetime, SSO settings)

- [ ] **4.9** OIDC/SSO integration
- `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` env vars
- Token validation against OIDC provider
- Map SSO subject to user record via `users.sso_subject`
- Role assignable to SSO identities per `FEATURE_REQUIREMENTS.md`

---

## Phase 5 — Agent Core

- [ ] **5.1** Agent binary entrypoint
- `agent/cmd/agent/main.go` — subcommand dispatch:
  - `agent run` — daemon mode (called by service manager)
  - `agent status`, `agent version` — local CLI commands
  - `agent cdm ...` — CDM management
  - `agent install`, `agent uninstall` — installer operations
  - `agent verify` — signature verification
- Config loading from TOML file per `INSTALLER_PACKAGING_SPEC.md` (`/etc/agent/config.toml` on Linux, `C:\ProgramData\Agent\config.toml` on Windows)

- [ ] **5.2** Platform abstraction layer (`agent/platform/`)
- Interface for OS-specific operations:
  - Service management (restart self)
  - Key storage paths
  - Config/cert/log paths
  - Socket/pipe paths
  - Package manager invocation
  - CA trust store installation
- `agent/platform/linux/` — systemd, PAM auth, Unix socket
- `agent/platform/windows/` — SCM, LogonUser, named pipe

- [ ] **5.3** TLS client configuration
- Load client cert + key from disk
- Load server CA from disk (received at enrollment)
- Configure `tls.Config` for mTLS on all server requests
- Hot-swap certificate on renewal without restarting the poller

- [ ] **5.4** Enrollment flow (agent side)
Per `AGENT_AUTH_SPEC.md`:
- Generate ECDSA keypair locally (private key never transmitted)
- Build CSR with hostname, OS, arch metadata
- `POST /v1/agents/enroll` with enrollment token + CSR
- Store returned cert + CA chain to disk
- Consume and delete enrollment token file
- On success, begin check-in loop

- [ ] **5.5** Certificate renewal (agent side)
- On each check-in, check if cert expiry is within 30 days
- If so, generate new keypair + CSR, `POST /v1/agents/renew`
- Swap to new cert on success
- If renewal fails repeatedly and cert expires, fall back to re-enrollment per `AGENT_AUTH_SPEC.md` re-enrollment flow

- [ ] **5.6** Check-in loop (`agent/poller/`)
Per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md`:
- Poll every `poll_interval_seconds` (default 30s, server-adjustable via check-in response `config` block)
- Build check-in request: `agent_id`, `timestamp`, monotonic `sequence`, `status` (uptime, CDM state, agent version), `inventory_delta`
- `POST /v1/agents/checkin`
- Parse response: process `jobs` array, apply `config` changes
- Dispatch received jobs to the executor

---

## Phase 6 — Job Lifecycle

- [ ] **6.1** Job state machine (server side, `server/jobs/`)
Implement full state machine per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md`:
- States: `PENDING` → `QUEUED` → `DISPATCHED` → `ACKNOWLEDGED` → `RUNNING` → `COMPLETED` / `FAILED` / `TIMED_OUT` / `CANCELLED`
- `CDM_HOLD` branch: if device has CDM enabled with no active session
- Dispatch timeout: auto-requeue `DISPATCHED` → `QUEUED` after one missed check-in (~60s)
- State transition validation (no skipping states, terminal states are final)
- All transitions written to DB with timestamps

- [ ] **6.2** Job creation endpoint
`POST /v1/jobs` (per `REST_API_SPEC.md`):
- Accept target: `device_ids`, `group_ids`, `tag_ids`, `site_ids`
- Resolve targets to individual device IDs (expand groups/tags/sites)
- Create one job record per device
- Per-type default retry policy per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md` table, overridable at creation
- Publish to NATS `jobs.dispatch.{tenant_id}.{device_id}`
- Return all created `job_ids` + `target_device_count`
- Audit-log

- [ ] **6.3** Job dispatch (worker)
- Worker consumes from NATS `jobs` stream
- On agent check-in (via API server call to worker logic): determine which queued jobs to include based on device CDM state
  - CDM disabled → dispatch normally
  - CDM enabled, no session → transition to `CDM_HOLD`
  - CDM enabled, session active → dispatch within session window
  - Session expiring (< 1 poll interval remaining) → dispatch no new jobs
- Mark dispatched jobs as `DISPATCHED` with timestamp

- [ ] **6.4** Job acknowledgement endpoint
`POST /v1/agents/jobs/{job_id}/acknowledge` (mTLS):
- Transition job to `ACKNOWLEDGED`
- Record `acknowledged_at` timestamp

- [ ] **6.5** Job result submission endpoint
`POST /v1/agents/jobs/{job_id}/result` (mTLS, per `REST_API_SPEC.md`):
- Accept status, exit_code, stdout, stderr, timestamps
- Transition job to terminal state (`COMPLETED`, `FAILED`, `TIMED_OUT`)
- Write to `job_results` table
- If `FAILED`/`TIMED_OUT` and retries remaining: create new job linked via `parent_job_id`, enqueue

- [ ] **6.6** Job management endpoints
- `GET /v1/jobs`, `GET /v1/jobs/{job_id}` — list/get with pagination and filters
- `POST /v1/jobs/{job_id}/cancel` — valid for `PENDING`, `QUEUED`, `CDM_HOLD`, `DISPATCHED`
- `POST /v1/jobs/{job_id}/retry` — valid for `FAILED`, `TIMED_OUT`; creates new linked job
- `GET /v1/devices/{device_id}/jobs` — device-scoped job list

- [ ] **6.7** Agent job executor (`agent/executor/`)
- Receive jobs from poller
- Acknowledge immediately (`POST /v1/agents/jobs/{job_id}/acknowledge`)
- Execute based on job type:
  - `exec` → run shell command with timeout, capture stdout/stderr/exit code
  - `inventory_full` → trigger full inventory collection
  - (other types wired in later phases)
- Submit result (`POST /v1/agents/jobs/{job_id}/result`)
- Sequential or bounded-concurrency execution

---

## Phase 7 — Device Management

- [ ] **7.1** Device CRUD endpoints
Per `REST_API_SPEC.md`:
- `GET /v1/devices` — list with filters (status, group_id, tag_id, site_id, os, search), cursor pagination
- `GET /v1/devices/{device_id}` — single device detail
- `PATCH /v1/devices/{device_id}` — operator metadata updates
- Device status derivation: `online` if `last_seen_at` within 2x poll interval, else `offline`

- [ ] **7.2** Groups endpoints
- `GET /v1/groups`, `POST /v1/groups`, `GET /v1/groups/{group_id}`, `PATCH /v1/groups/{group_id}`, `DELETE /v1/groups/{group_id}`
- `GET /v1/groups/{group_id}/devices` — list members
- `POST /v1/groups/{group_id}/devices` — add devices
- `DELETE /v1/groups/{group_id}/devices/{device_id}` — remove device

- [ ] **7.3** Tags endpoints
- `GET /v1/tags`, `POST /v1/tags`, `DELETE /v1/tags/{tag_id}`
- `POST /v1/devices/{device_id}/tags`, `DELETE /v1/devices/{device_id}/tags/{tag_id}`

- [ ] **7.4** Sites endpoints
- `GET /v1/sites`, `POST /v1/sites`, `GET /v1/sites/{site_id}`, `PATCH /v1/sites/{site_id}`, `DELETE /v1/sites/{site_id}`
- `GET /v1/sites/{site_id}/devices`
- `POST /v1/sites/{site_id}/devices`, `DELETE /v1/sites/{site_id}/devices/{device_id}`

- [ ] **7.5** Enrollment token endpoints
- `GET /v1/enrollment-tokens` — list (no token values, only metadata)
- `POST /v1/enrollment-tokens` — create, return token value once
- `DELETE /v1/enrollment-tokens/{token_id}` — revoke

- [ ] **7.6** Device inventory endpoint
- `GET /v1/devices/{device_id}/inventory` — hardware + packages

- [ ] **7.7** Device logs endpoint
- `GET /v1/devices/{device_id}/logs` — paginated log entries with level/time filters

---

## Phase 8 — Inventory Collection

- [ ] **8.1** Hardware inventory collector (`agent/inventory/`)
- CPU: model, cores, threads
- RAM: total MB
- Disks: device, size, type, mount point
- Network interfaces: name, MAC, IPs
- Platform-specific implementations in `agent/platform/{linux,windows}/`

- [ ] **8.2** Software/package inventory collector
- Linux: `dpkg-query` (Debian/Ubuntu), `rpm -qa` (RHEL/Fedora)
- Windows: WMI `Win32_Product` or registry-based enumeration
- Return: package name, version, manager, installed_at

- [ ] **8.3** Delta inventory computation
- Compare current inventory snapshot against last-sent snapshot
- Produce delta: `packages.added`, `packages.removed`, `packages.updated`
- Include delta in check-in request body (omit if empty per spec)

- [ ] **8.4** Full inventory job handler
- Handle `inventory_full` job type in executor
- Collect full hardware + software inventory
- Ship as job result
- Server stores to `inventory_hardware` and `inventory_packages` tables
- Server can request on-demand via job queue

- [ ] **8.5** Server-side inventory storage
- Upsert hardware inventory on full collection
- Upsert/insert/delete packages based on delta
- Track `last_seen_at` on packages for staleness detection

---

## Phase 9 — CDM (Customer Device Mode)

- [ ] **9.1** CDM state machine (`agent/cdm/`)
Per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md` and `LOCAL_UI_CLI_SPEC.md`:
- States: disabled, enabled (no session), enabled (session active)
- Session properties: duration, expires_at, granted_by, granted_at
- Session grant: set expiry, start allowing job execution
- Session revoke: immediate, in-flight jobs complete, no new jobs accepted
- Session expiry: same as revoke, triggered by timer
- CDM toggle: enable/disable CDM entirely
- State is local-authoritative — persisted to disk, reported to server via check-in

- [ ] **9.2** CDM integration with executor
- Before executing a received job, check CDM state
- If CDM enabled and no session → hold job locally (report to server as CDM_HOLD on next check-in)
- If CDM enabled and session active → execute within session window
- If session expires during execution → in-flight job completes, no new jobs started

- [ ] **9.3** CDM state in check-in
- Always include `cdm_enabled`, `cdm_session_active`, `cdm_session_expires_at` in check-in status
- Server mirrors these fields on the device record
- Server uses CDM state to decide which jobs to dispatch per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md` CDM signaling table

- [ ] **9.4** CDM audit logging
- Local audit log entries for: toggle, session grant, session revoke, session expiry, per-job execution during session
- Stored in root/SYSTEM-only readable file on device

---

## Phase 10 — Log Shipping

- [ ] **10.1** Agent log shipping
`POST /v1/agents/logs` (mTLS, per `REST_API_SPEC.md`):
- Agent periodically ships log entries to server
- Flows regardless of CDM state (not gated by CDM)

- [ ] **10.2** Server log ingestion
- API server receives log entries, publishes to NATS `logs.{tenant_id}.{device_id}`
- Worker consumes and writes to a log store (PostgreSQL or dedicated log table)

- [ ] **10.3** Audit log endpoint
`GET /v1/audit-log` per `REST_API_SPEC.md`:
- Cursor pagination
- Filters: `actor_id`, `actor_type`, `action`, `resource_type`, `resource_id`, `since`, `until`
- Append-only, read-only — no delete/update endpoints

---

## Phase 11 — File Transfer

- [ ] **11.1** Storage backend abstraction
Per `FILE_TRANSFER_SPEC.md`:
- Interface: `Store`, `Upload`, `Download`, `Delete`
- `server` backend: local filesystem or mounted volume
- `s3` backend: S3-compatible (AWS S3, MinIO, R2) with pre-signed URL generation
- Backend selection per tenant config

- [ ] **11.2** Signing key management
- `POST /v1/signing-keys` — register Ed25519 public key
- `GET /v1/signing-keys` — list
- `DELETE /v1/signing-keys/{key_id}` — fails if referenced by files
- Only Ed25519 supported

- [ ] **11.3** Chunked file upload
Per `FILE_TRANSFER_SPEC.md` upload flow:
- `POST /v1/files` — initiate upload, get `upload_id`, chunk count
- `PUT /v1/files/uploads/{upload_id}/chunks/{chunk_index}` — upload individual chunk with `X-Chunk-SHA256`
- `GET /v1/files/uploads/{upload_id}` — resume (list uploaded chunks)
- `POST /v1/files/uploads/{upload_id}/complete` — assemble, verify full-file SHA-256, verify signature if provided
- Upload sessions expire after 24h inactivity

- [ ] **11.4** File management endpoints
- `GET /v1/files` — list file metadata (paginated, searchable)
- `GET /v1/files/{file_id}` — file metadata
- `DELETE /v1/files/{file_id}` — fails if referenced by pending/active transfer jobs

- [ ] **11.5** File download endpoint
`GET /v1/files/{file_id}/download` (mTLS for agents):
- Server backend: return short-lived download URL (5 min) + chunk metadata
- S3 backend: return pre-signed URL (1 hour) + chunk metadata

- [ ] **11.6** Agent file transfer handler
Handle `file_transfer` job type in executor per `FILE_TRANSFER_SPEC.md` agent flow:
- Pre-flight: check free disk space against threshold
- Acknowledge job
- `GET /v1/files/{file_id}/download` to get URL
- Download in chunks using Range requests
- Verify SHA-256 if `require_sha256: true`
- Verify Ed25519 signature if `require_signature: true`
- Move file to drop directory
- Execute `on_complete` command if specified
- Submit result with appropriate error codes on failure

- [ ] **11.7** Agent-side storage config
- Drop directory: `/opt/agent/drop` (Linux), `C:\ProgramData\Agent\Drop` (Windows)
- Space check: configurable threshold (default 50% of free space)
- Overridable per-job via payload `storage` block

---

## Phase 12 — Package Management Jobs

- [ ] **12.1** Package manager abstraction (`agent/platform/`)
- Interface: `Install(name, version)`, `Remove(name)`, `Update(name, version)`
- Linux: `apt-get` (Debian/Ubuntu), `dnf` (RHEL/Fedora) — detect at runtime
- Windows: `msiexec`, `winget`, or Chocolatey — detect at runtime
- Runs via minimal setuid helper binary (not as root agent process per `INSTALLER_PACKAGING_SPEC.md`)

- [ ] **12.2** Package job handlers in executor
- `package_install` — install package via native manager, capture output
- `package_remove` — remove package
- `package_update` — update package to specified or latest version
- Retry policies per `AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md` defaults (3 retries, 5 min backoff)
- After success, trigger delta inventory on next check-in

---

## Phase 13 — Agent Update

- [ ] **13.1** Agent version registry (server)
Per `AGENT_UPDATE_SPEC.md`:
- `POST /v1/agent-versions` — publish version with per-platform binaries (sha256, Ed25519 signature, file_id)
- `GET /v1/agent-versions` — list (filter by channel, paginated)
- `GET /v1/agent-versions/{version}`
- `POST /v1/agent-versions/{version}/yank` — suppress from auto-update, warn on manual

- [ ] **13.2** Auto-update policy management
- Tenant-level default + group-level override in `agent_update_policies` table
- Policy fields: enabled, channel, schedule (cron), rollout strategy (immediate/gradual), batch_percent, batch_interval_minutes
- Scheduler evaluates policies when new versions are published

- [ ] **13.3** Gradual rollout controller (scheduler)
Per `AGENT_UPDATE_SPEC.md` rollout section:
- Select batch_percent% of devices randomly (seeded per rollout)
- Enqueue `agent_update` jobs for batch
- Wait `batch_interval_minutes`, check rollback rate
- If rollback rate > 10% → pause rollout, alert operator
- `GET /v1/agent-versions/{version}/rollout` — status
- `POST .../rollout/pause`, `.../rollout/resume`, `.../rollout/abort`

- [ ] **13.4** Agent update handler
Handle `agent_update` job in executor per `AGENT_UPDATE_SPEC.md` flow:
1. Pre-flight: version check (no downgrades unless `force: true`), disk space check, current binary path check
2. Download new binary in chunks (reuse file transfer download logic)
3. Verify SHA-256 + Ed25519 signature (both mandatory for updates)
4. Side-by-side staging: copy current → `agent.previous`, rename new → `agent` (atomic via `rename`/`MoveFileEx`)
5. Write `pending_update.json` with job_id, expected_version, deadline (3x poll interval)
6. Submit partial result (`status: restarting`)
7. Signal service manager to restart

- [ ] **13.5** Post-restart verification (agent)
- On startup, check for `pending_update.json`
- Verify own version matches `expected_version`
- If match: complete update job, delete verification file
- If mismatch or deadline exceeded: trigger automatic rollback

- [ ] **13.6** Automatic rollback
- Rename `agent.previous` back to `agent` atomically
- Restart into restored binary
- Report rollback on next check-in (`last_update_failed: true`, error details)
- Server marks update job `FAILED`, suppresses auto-update retries until operator review

- [ ] **13.7** Manual rollback endpoint
`POST /v1/devices/{device_id}/rollback` per `AGENT_UPDATE_SPEC.md`:
- Enqueue `agent_rollback` job
- Agent verifies `agent.previous` exists and meets `min_rollback_version`
- Swap binaries, restart, report success

---

## Phase 14 — Scheduling & Alerting

- [ ] **14.1** Scheduler process
Per `SERVER_DEPLOYMENT_SPEC.md`:
- Single active instance via PostgreSQL advisory lock (leader election)
- Tick loop evaluating cron expressions and timed events
- Second replica can stand by for failover

- [ ] **14.2** Scheduled jobs
- Evaluate `scheduled_jobs` table cron expressions against current time
- When due: resolve target (device/group/tag/site), create individual jobs via NATS
- Update `last_run_at`, compute `next_run_at`
- Respect enabled/disabled flag

- [ ] **14.3** Scheduled job endpoints
Per `REST_API_SPEC.md`:
- `GET /v1/scheduled-jobs`, `POST /v1/scheduled-jobs`
- `GET /v1/scheduled-jobs/{id}`, `PATCH /v1/scheduled-jobs/{id}`, `DELETE /v1/scheduled-jobs/{id}`
- `POST /v1/scheduled-jobs/{id}/enable`, `POST /v1/scheduled-jobs/{id}/disable`

- [ ] **14.4** Alert rule engine
- Scheduler monitors device `last_seen_at` timestamps
- Evaluate alert rule conditions (e.g. `agent_offline` with `threshold_minutes`)
- Fire matching rules through configured channels

- [ ] **14.5** Alert notification channels
- Webhook: POST JSON payload to configured URLs
- Email: send via SMTP
- Per `REST_API_SPEC.md` alert rule format: condition + channels + scope

- [ ] **14.6** Alert rule endpoints
- `GET /v1/alert-rules`, `POST /v1/alert-rules`
- `GET /v1/alert-rules/{rule_id}`, `PATCH /v1/alert-rules/{rule_id}`, `DELETE /v1/alert-rules/{rule_id}`
- `POST /v1/alert-rules/{rule_id}/enable`, `POST /v1/alert-rules/{rule_id}/disable`

---

## Phase 15 — Agent Local Interfaces

- [ ] **15.1** Local IPC transport
Per `LOCAL_UI_CLI_SPEC.md`:
- Linux: Unix socket at `/run/agent/agent.sock` (mode `0660`, group `agent-users`)
- Windows: named pipe `\\.\pipe\agent` (ACL: SYSTEM + Administrators)
- Internal request/response protocol over socket/pipe (JSON-RPC or similar)

- [ ] **15.2** Local authentication
Per `LOCAL_UI_CLI_SPEC.md`:
- Linux: PAM authentication via `go-pam`
- Windows: `LogonUser()` via `advapi32.dll` syscall
- On success, issue short-lived session token (CLI: 15 min idle-refreshed; Web: 30 min idle / 8hr absolute)

- [ ] **15.3** Local web UI server (`agent/localui/`)
Per `LOCAL_UI_CLI_SPEC.md`:
- Bind to `127.0.0.1:57000` only (configurable port)
- HTTPS mandatory — TLS with per-device local CA
- Generate unique ECDSA P-256 CA on device at install time (Name Constraints: localhost, 127.0.0.1 only)
- Install CA into OS trust store (Debian/Ubuntu, RHEL/Fedora, Windows CertMgr)
- Issue 90-day localhost cert signed by local CA, auto-rotate
- Session cookie: HttpOnly, Secure, SameSite=Strict
- Serve login page, status page, CDM management UI
- Static embedded frontend (Go `embed`)

- [ ] **15.4** Local CLI commands (`agent/localcli/`)
Per `LOCAL_UI_CLI_SPEC.md` and `INSTALLER_PACKAGING_SPEC.md` CLI reference:
- `agent status` — agent status, version, config
- `agent cdm status` — CDM state, pending jobs
- `agent cdm enable`, `agent cdm disable`
- `agent cdm grant --duration <duration>`
- `agent cdm revoke`
- `agent logs [--tail N]`
- `agent version`
- All commands authenticate via socket/pipe using OS credentials or cached session token

- [ ] **15.5** Local audit logging
Per `LOCAL_UI_CLI_SPEC.md`:
- Write to local file (root/SYSTEM only readable)
- Events: auth success/failure, CDM toggle, CDM session grant/revoke, config view
- Filtered CDM-only view exposed in UI and CLI for local user

---

## Phase 16 — Installer & Packaging

- [ ] **16.1** Linux install script (`install.sh`)
Per `INSTALLER_PACKAGING_SPEC.md`:
- Detect init system (require systemd)
- Create `agent:agent` system user/group
- Install binary to `/usr/local/bin/agent`
- Create directory structure with correct ownership/permissions
- Write enrollment token, server URL, CA cert to config
- Install systemd unit file (with hardening: NoNewPrivileges, ProtectSystem=strict, etc.)
- `systemctl daemon-reload && systemctl enable --now agent`
- Wait 30s for first check-in, report result

- [ ] **16.2** Linux uninstall
- `agent uninstall` — soft: stop service, remove binary + unit file, retain config/certs
- `agent uninstall --purge` — also remove `/etc/agent/`, `/var/lib/agent/`, `/var/log/agent/`

- [ ] **16.3** Windows MSI installer (WiX v4)
Per `INSTALLER_PACKAGING_SPEC.md`:
- Package binary, service registration, custom install actions
- MSI flags: `ENROLLMENT_TOKEN`, `SERVER_URL`, `CDM_ENABLED`
- Create `NT SERVICE\Agent` service account
- Install CA cert into Local Machine\Root store
- Register + start Windows Service (automatic, restart on failure)

- [ ] **16.4** Windows uninstall
- Soft: `msiexec /x ... /quiet` — remove binary + service, retain ProgramData
- Purge: `msiexec /x ... /quiet PURGE=1` — also remove ProgramData + CA cert from store

- [ ] **16.5** Install script endpoint
`GET /v1/install/{os}/{arch}?token=<enrollment_token>` per `INSTALLER_PACKAGING_SPEC.md`:
- Return shell script (Linux) or PowerShell script (Windows) that:
  - Downloads latest stable installer
  - Verifies SHA-256 + Ed25519 signature
  - Runs installer with enrollment token embedded (not in CLI args)
- `POST /v1/enrollment-tokens/{token_id}/install-command` — generate one-liner

- [ ] **16.6** Installer hosting endpoints
- `GET /v1/installers` — list available installers
- `GET /v1/installers/{os}/{arch}/{version}` — download specific
- `GET /v1/installers/{os}/{arch}/latest` — download latest stable
- `GET /v1/installers/{os}/{arch}/{version}/checksum` — SHA-256
- `GET /v1/installers/{os}/{arch}/{version}/signature` — Ed25519 sig
- `POST /v1/installers` — upload new installer (admin)

- [ ] **16.7** Setuid helper binary
Per `INSTALLER_PACKAGING_SPEC.md` security note:
- Minimal binary for package manager invocations (apt, dnf)
- Setuid root, called by the non-root agent process
- Restricts allowed operations to package management commands only

---

## Phase 17 — Deployment Configs

- [ ] **17.1** PostgreSQL migration framework
- Integrate `golang-migrate` (per `SERVER_DEPLOYMENT_SPEC.md`)
- Freeze `deploy/schema.sql` into `deploy/migrations/0001_initial_schema.sql`
- Forward-only numbered SQL files in `deploy/migrations/`
- `migrate` subcommand on the API server binary: `moebius-api migrate`
- Idempotent — safe to run on every deploy

- [ ] **17.2** Docker Compose deployment
Per `SERVER_DEPLOYMENT_SPEC.md`:
- `deploy/docker-compose.yml` with services: postgres, nats, api, worker, scheduler, proxy (Caddy)
- `deploy/.env.example` with required/optional vars
- `deploy/Caddyfile` for reverse proxy + auto-TLS
- Healthchecks on postgres
- Volume mounts for persistent data

- [x] **17.3** Dockerfiles
- `Dockerfile.api`, `Dockerfile.worker`, `Dockerfile.scheduler`
- Multi-stage build: Go builder → alpine runtime
- Entrypoint with subcommand support (`migrate`, `generate-ca`, `create-admin`)
- *(completed in Phase 0)*

- [ ] **17.4** Helm chart
Per `SERVER_DEPLOYMENT_SPEC.md` Helm structure:
- `deploy/helm/charts/moebius/`
- Templates: api (deployment, service, HPA, PDB), worker (deployment, HPA, PDB), scheduler (deployment), ingress, configmap, secrets, migration job
- `values.yaml` with defaults, `values.production.yaml` with SaaS overrides
- NATS: bundled StatefulSet or external URL
- PostgreSQL: external only (managed DB)
- Ingress: nginx class, cert-manager annotation
- mTLS passthrough for `/v1/agents/*` paths (Option A per spec)
- Pre-upgrade hook for migrations

- [ ] **17.5** Create-admin command
`moebius-api create-admin --email <email>` per `SERVER_DEPLOYMENT_SPEC.md` startup sequence:
- Create initial admin user + Super Admin role
- Generate and print initial API key
- First-run bootstrap only

---

## Phase 18 — Web UI (React)

- [ ] **18.1** UI project scaffolding
- `ui/` — React SPA (Vite or similar)
- Auth: API key or OIDC token against server REST API
- Routing, layout, auth context

- [ ] **18.2** Device management views
Per `FEATURE_REQUIREMENTS.md`:
- Device list: live status, search, filter by group/tag/site/OS, cursor pagination
- Device detail: inventory (hardware + packages), job history, logs, CDM state
- Group/tag/site management

- [ ] **18.3** Job management views
- Job creation: select type, target (devices/groups/tags/sites), payload, retry policy
- Job monitoring: list with status filters, detail view with result output
- Cancel/retry actions

- [ ] **18.4** Scheduled job management
- Create/edit/delete scheduled jobs with cron expression builder
- Enable/disable toggle
- Run history

- [ ] **18.5** File transfer UI
- File upload with progress (chunked upload)
- Create file transfer jobs
- Transfer status monitoring

- [ ] **18.6** Admin views
- User management: invite, assign roles, deactivate
- Role management: create/edit custom roles, permission picker
- API key management: create (show once), revoke
- Enrollment token management: create (show once), revoke
- Alert rule configuration
- Tenant settings (poll interval, CDM defaults, SSO config)
- Audit log viewer with filters

- [ ] **18.7** Add Device flow
Per `INSTALLER_PACKAGING_SPEC.md`:
- Devices → Add Device → Select OS/Arch → generate enrollment token → display one-liner install command

---

## Phase 19 — Release Pipeline

- [ ] **19.1** GitHub Actions: CI
- On PR: lint (golangci-lint), unit tests, integration tests (with postgres + nats containers)
- Go build matrix (all targets) to verify compilation

- [ ] **19.2** GitHub Actions: Release
Per `INSTALLER_PACKAGING_SPEC.md` pipeline:
- On tag push (`v*`):
  - Build agent binaries: linux/amd64, linux/arm64, windows/amd64
  - Package: tar.gz (Linux), MSI (Windows via WiX)
  - Compute SHA-256 checksums
  - Sign all artifacts with Ed25519 release key (from GitHub secrets)
  - Build + push server container images (api, worker, scheduler) for linux/amd64 + linux/arm64
  - Sign images with cosign
  - Create GitHub Release with all artifacts
  - Optionally upload to management server and register as new agent version

- [ ] **19.3** Release signing key
- Generate Ed25519 keypair
- Private key → GitHub Actions secret
- Public key → `keys/release.pub` in repo + registered as signing key on server

---

## Phase 20 — Integration Testing & Hardening

- [ ] **20.1** End-to-end enrollment test
- Start server stack (postgres, nats, api, worker, scheduler)
- Create enrollment token
- Agent enrolls, receives cert
- Agent performs first check-in
- Verify device appears in server with correct inventory

- [ ] **20.2** Job lifecycle end-to-end
- Create exec job targeting enrolled device
- Agent receives job on next check-in, acknowledges, executes
- Verify job result appears in server
- Test retry flow: job fails, retries per policy
- Test cancellation

- [ ] **20.3** CDM end-to-end
- Enable CDM on device, verify jobs go to CDM_HOLD
- Grant session, verify jobs dispatch
- Revoke session, verify new jobs held
- Session expiry

- [ ] **20.4** File transfer end-to-end
- Upload file via chunked API
- Create file_transfer job
- Agent downloads, verifies, stores in drop directory
- Test on_complete command execution

- [ ] **20.5** Agent update end-to-end
- Publish new agent version
- Trigger manual update job
- Agent downloads, verifies, stages, restarts
- Post-restart verification succeeds
- Test rollback on version mismatch

- [ ] **20.6** Certificate lifecycle
- Agent cert renewal before expiry
- Agent behavior on revocation (re-enrollment flow)
- Expired cert handling

- [ ] **20.7** Multi-tenancy isolation
- Create two tenants
- Verify no cross-tenant data access through API
- Verify tenant-scoped queries return correct results

- [ ] **20.8** RBAC enforcement
- Test each predefined role against all endpoints
- Verify scope restrictions (group/tag/site) filter results correctly
- Test custom roles with partial permissions
