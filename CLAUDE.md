# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Moebius is a FOSS device management platform (agent + server) written in Go. It manages Windows and Linux endpoints at scale using a poll-based architecture with mTLS authentication.

**Module:** `github.com/eavalenzuela/Moebius`

## Architecture

- **API Server (Go):** Single public-facing component. REST API with API key + OIDC auth, RBAC enforcement, inline job dispatch. Serves agent check-in endpoint.
- **Scheduler (Go):** Leader-elected background process. Evaluates cron-scheduled jobs, fires alert rules, and reaps stuck jobs / expired tokens. One active instance per cluster via PG advisory lock.
- **PostgreSQL:** Single source of truth. Tenant isolation via `tenant_id` on every table.
- **Agent (Go):** Polls server every 30s, ships heartbeat + delta inventory, receives and executes jobs. Runs as systemd service (Linux) or Windows Service. Includes local web UI (localhost-only) and CLI.
- **Web UI (React):** SPA that talks only to the API server.

Job dispatch is **inline** (API server assigns jobs during check-in) rather than via a message bus. Phase 6 removed NATS and the separate worker binary — the scheduler now absorbs the remaining background responsibilities (reaping stuck jobs, cron evaluation, alert evaluation).

Key invariants:
- Agents never receive inbound connections (poll-only)
- CDM (Customer Device Mode) is enforced on the agent, not the server
- All RBAC enforcement is in the API server; background processors (scheduler) trust pre-authorized rows

## Monorepo Structure

```
agent/          # Agent binary: poller, executor, inventory, cdm, localui, localcli, platform/{windows,linux}
server/         # Server: api, auth, rbac, jobs, scheduler, store, notify
shared/         # protocol (agent<->server types), models, version
ui/             # React frontend
deploy/         # docker-compose.yml, helm/, migrations/
tools/          # Release signing utilities (keygen, sign)
tests/          # Integration tests (build tag: integration)
```

## Documentation

User-facing guides:
- `docs/Deployment_Instructions.md` — local dev, Docker Compose, Kubernetes/Helm, env vars, upgrades
- `docs/User_Guide.md` — operator guide: enrollment, devices, jobs, CDM, alerts, RBAC
- `docs/KEY_ROTATION.md` — canonical rotation procedures for CAs, signing keys, API keys, DB password
- `docs/AUDIT_RETENTION.md` — audit log retention, pruning around the append-only rules, partitioning
- `SECURITY.md` — security architecture and design decisions

Design specs live in `docs/`. Key references when implementing:
- `docs/HIGH-LEVEL_DESIGN.md` — architecture, component responsibilities, directory layout
- `docs/AGENT_AUTH_SPEC.md` — mTLS enrollment, certificate lifecycle
- `docs/AGENT_CHECKIN_AND_CORE_DESIGN_SPEC.md` — check-in protocol, job lifecycle state machine
- `docs/REST_API_SPEC.md` — full API endpoint reference
- `docs/SERVER_DEPLOYMENT_SPEC.md` — Docker Compose (self-hosted) and Kubernetes/Helm (SaaS) deployment
- `docs/LOCAL_UI_CLI_SPEC.md` — agent local web UI and CLI interfaces
- `docs/FILE_TRANSFER_SPEC.md` — chunked file upload/download with signature verification
- `docs/AGENT_UPDATE_SPEC.md` — agent auto-update with rollback
- `docs/INSTALLER_PACKAGING_SPEC.md` — agent distribution and installation

Progress tracking at root: `FEATURE_REQUIREMENTS.md`, `IMPLEMENTATION_PLAN.md`.

## Build & Development

```bash
make build          # Build all binaries (native) to dist/
make test           # Run unit tests with race detector
make lint           # Run golangci-lint
make vet            # Run go vet
make fmt            # Format code
make fmt-check      # Check formatting (CI)
make test-cover     # Tests with coverage report
make build-agent-all  # Cross-compile agent for linux/{amd64,arm64}, windows/amd64
make build-server-all # Cross-compile server binaries for linux/{amd64,arm64}
make docker-build   # Build Docker images for api, scheduler, ui
make clean          # Remove build artifacts
```

Version is injected at build time via `-ldflags` from `shared/version` package.

**Three binaries:**
- `server/cmd/api` → `moebius-api` (subcommands: `migrate`, `generate-ca`, `create-admin`)
- `server/cmd/scheduler` → `moebius-scheduler` (cron evaluation, alert firing, job/token reaping)
- `agent/cmd/agent` → `moebius-agent` (subcommands: `run`, `status`, `cdm`, `install`, `uninstall`, `verify`, `logs`, `version`)

**Server core packages (Phase 2):**
- `server/config` — env var config loading with per-process validation
- `server/store` — pgxpool wrapper (repository methods added per phase)
- `server/logging` — slog-based structured logging (JSON/text, configurable level)
- `server/metrics` — Prometheus metric definitions (counters, gauges, histograms)
- `server/health` — liveness (`/health`) and readiness (`/health/ready`) HTTP handlers
- `server/audit` — append-only audit log writes to PostgreSQL

**Server infrastructure (Phase 6.0):**
- `server/migrate` — embedded SQL migration runner using Go `embed.FS`. Owns the `schema_migrations` table (migration SQL files must NOT create or insert into it). Files in `server/migrate/sql/` (Go embed) and `deploy/migrations/` (canonical copy).
- `server/cmd/api/main.go` — wires `runServer()`, `runMigrate()`, `runCreateAdmin()`, `runGenerateCA()`. Uses `health.New(map[string]health.Checker{"database": st})` for health checks. Functions that connect to DB and also call `os.Exit` must extract logic into a `doXxx() error` helper to avoid gocritic `exitAfterDefer` (defer won't run if os.Exit is called).
- Bootstrap admin (`create-admin` subcommand) — creates default tenant (slug "default"), Super Admin role with `rbac.SuperAdminPermissions`, admin user (admin@localhost), and `sk_`-prefixed API key printed once.

**Job lifecycle (Phase 6):**
- `server/jobs/` — pure state machine logic: `ValidateTransition()`, `ValidateType()`, `DefaultRetryPolicy()`, `ShouldRetry()`, `IsCancellable()`. No DB dependency.
- `server/api/checkin.go` — `POST /v1/agents/checkin` (mTLS). Updates device, auto-requeues stale dispatched jobs, applies CDM hold/release logic, dispatches up to 10 queued jobs.
- `server/api/jobs.go` — `POST /v1/jobs` (API key auth). Resolves targets (groups/tags/sites → device IDs), creates one job per device with default retry policies. Also `GET /v1/jobs`, `GET /v1/jobs/{job_id}`, `POST .../cancel`, `POST .../retry`.
- `server/api/agent_jobs.go` — `POST /v1/agents/jobs/{job_id}/acknowledge` and `.../result` (mTLS). Result handler stores to `job_results`, auto-retries via new linked job with `parent_job_id`.
- `agent/executor/` — receives jobs from poller, acknowledges, executes (`exec` type: shell with timeout), reports results. Wired into `agent/cmd/agent/main.go` via `exec.HandleJob` as poller's `JobHandler`.

**Stack:** Go, PostgreSQL. Migrations in `deploy/migrations/`. Docker images in `deploy/docker/`.
