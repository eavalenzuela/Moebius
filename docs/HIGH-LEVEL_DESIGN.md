## High-level Architecture

### Components

```
┌─────────────────────────────────────────────────────┐
│                    Client Layer                      │
│         Web UI (React)    CLI (Go)                  │
└────────────────────┬────────────────────────────────┘
                     │ HTTPS REST
┌────────────────────▼────────────────────────────────┐
│                   API Server (Go)                    │
│  - REST API + auth (API keys, OIDC/SSO)             │
│  - mTLS validation for agent endpoints              │
│  - RBAC enforcement                                  │
│  - Inline job dispatch (on agent check-in)           │
│  - Agent check-in + enrollment                       │
│  - File upload handling                              │
│  - Tenant management                                 │
└──────┬──────────────────────────────────────────────┘
       │
┌──────▼─────────────────────────────────────────────┐
│                   PostgreSQL                         │
│  devices, inventory, jobs, job_results, audit_log,   │
│  RBAC, tenants, scheduled_jobs, alert_rules, ...     │
└──────▲──────────────────────────────────────────────┘
       │
┌──────┴──────────────────────────────────┐
│             Scheduler (Go)               │
│  - Leader-elected via PG advisory lock   │
│  - Cron-scheduled job enqueue            │
│  - Alert-rule evaluation + notifications │
│  - Reaper: stuck dispatched → requeue    │
│  - Reaper: stuck in-flight → timed_out   │
│  - Reaper: expired enrollment tokens     │
└──────────────────────────────────────────┘

                        ▲ HTTPS polling (poll-only, no inbound to agents)
        ┌───────────────┼───────────────┐
┌───────▼──────┐ ┌──────▼──────┐ ┌─────▼───────┐
│   Agent      │ │   Agent     │ │   Agent     │
│  (Windows)   │ │  (Linux)    │ │  (Linux)    │
│  local UI    │ │  local UI   │ │  local UI   │
│  local CLI   │ │  local CLI  │ │  local CLI  │
└──────────────┘ └─────────────┘ └─────────────┘
```

### Component Responsibilities

#### API Server (`server/cmd/api`)

Single entrypoint for all external traffic (UI, CLI, third-party).
Authenticates every request (API key or OIDC token for users; mTLS client certificate for agents).
Enforces RBAC + scope before any data access or job creation.
Dispatches jobs **inline** during agent check-in — no message bus. The check-in handler picks up to N queued jobs for the checking-in device, applies CDM hold/release, and returns them in the response.
Serves agent check-in and enrollment endpoints.
Handles chunked file uploads and download URL generation.
Subcommands: `migrate`, `generate-ca`, `create-admin`.

#### Scheduler (`server/cmd/scheduler`)

Single active instance via PostgreSQL advisory lock (leader election); a second replica can stand by for failover. Absorbs the background work that was previously split across a separate worker binary — Phase 6 collapsed NATS + the worker into inline dispatch + this single scheduler.

Responsibilities per tick (default: 30s):
- **Scheduled-job evaluation** — walks `scheduled_jobs` rows whose `next_run_at` has passed, resolves targets (devices/groups/tags/sites) to device IDs, and inserts one `queued` job per device. Then advances `last_run_at` / `next_run_at` via the cron expression.
- **Alert-rule evaluation** — checks `alert_rules` conditions (e.g., device offline > threshold minutes) and fires webhook + email notifications via `server/notify`.
- **Reaping stuck `dispatched` jobs** — requeues jobs whose `dispatched_at` is older than `REAPER_DISPATCHED_TIMEOUT_SECONDS` (default 300). These are jobs the server handed out on check-in but the agent never acknowledged (usually because the agent went offline mid-poll). Clears `dispatched_at` so the next check-in can re-dispatch.
- **Reaping stuck in-flight jobs** — transitions `acknowledged`/`running` jobs whose `started_at`/`acknowledged_at` is older than `REAPER_INFLIGHT_TIMEOUT_SECONDS` (default 3600) to `timed_out` with a synthetic `last_error`. Bounds the lifetime of an abandoned job.
- **Reaping expired enrollment tokens** — deletes unused enrollment tokens past their `expires_at`. Used tokens (those with `used_at` set) are retained for audit.

The scheduler never consults RBAC — it operates on rows whose creation was already authorized through the API. The `server/cmd/scheduler` package has a regression test (`TestScheduler_NoAuthzImports`) that fails if it transitively imports `server/rbac`, `server/auth`, or `server/api`.

#### PostgreSQL

Single source of truth for all persistent state.
Key tables: `tenants`, `devices`, `inventory_hardware`, `inventory_packages`, `jobs`, `job_results`, `scheduled_jobs`, `audit_log`, `users`, `roles`, `api_keys`, `groups`, `tags`, `sites`, `agent_certificates`, `enrollment_tokens`, `files`, `signing_keys`, `agent_versions`, `agent_update_policies`, `alert_rules`, `installers`.
Forward-only numbered SQL migrations in `deploy/migrations/`.

#### Agent (`agent/cmd/agent`)

Polls API server every 30s (server-adjustable via check-in response).
On each poll: ships heartbeat + delta inventory, receives pending jobs.
Executes jobs sequentially or with bounded concurrency.
CDM state machine lives entirely on the agent (local-authoritative).
Local UI: HTTPS web server bound to `127.0.0.1:57000`, per-device CA with Name Constraints.
Local CLI: communicates via Unix socket (Linux) or named pipe (Windows).
Subcommands: `run`, `status`, `cdm`, `install`, `uninstall`, `verify`, `logs`, `version`.

#### Web UI

React SPA, talks only to the API server.
No direct agent or DB access.

### Monorepo Structure

```
/
├── agent/
│   ├── cmd/agent/       # agent binary entrypoint
│   ├── poller/          # check-in loop
│   ├── executor/        # job execution engine
│   ├── inventory/       # hardware + software collection
│   ├── cdm/             # customer device mode state machine
│   ├── localui/         # localhost web UI server
│   ├── localcli/        # local CLI commands
│   ├── update/          # agent self-update + rollback
│   └── platform/
│       ├── linux/       # systemd, PAM, Unix socket, apt/dnf
│       └── windows/     # SCM, LogonUser, named pipe, msiexec
├── server/
│   ├── cmd/
│   │   ├── api/         # API server binary
│   │   └── scheduler/   # scheduler binary (cron + reaper + alerts)
│   ├── api/             # REST handlers (including inline job dispatch)
│   ├── auth/            # API key + OIDC + mTLS
│   ├── rbac/            # role + scope enforcement
│   ├── jobs/            # job state-machine (pure functions)
│   ├── scheduler/       # scheduler logic (cron, reapers, alert eval)
│   ├── store/           # PostgreSQL data layer
│   └── notify/          # alerting + webhooks
├── shared/
│   ├── protocol/        # agent<->server request/response types
│   ├── models/          # shared domain types
│   └── version/         # build-time version injection
├── ui/                  # React frontend
├── cli/                 # Admin CLI (server-side)
├── deploy/
│   ├── docker/          # Dockerfiles for api, scheduler
│   ├── docker-compose.yml
│   ├── helm/            # Kubernetes Helm chart for SaaS deployment
│   └── migrations/      # PostgreSQL schema migrations (forward-only)
├── keys/                # release.pub (Ed25519 public key for artifact signing)
└── .github/workflows/   # CI (lint, test, build) + release pipeline
```

### Key Design Principles

- API server is the only component with a public network surface — scheduler and DB are internal only
- Agents never receive inbound connections — poll-only means no open ports on endpoints
- CDM is enforced on the agent, not the server — server cannot bypass it even if compromised
- All RBAC enforcement is in the API server — the scheduler trusts rows whose creation was already authorized
- Tenant isolation at the DB layer — every table with tenant-scoped data carries a `tenant_id` and queries always filter by it
- Server is two processes (API, scheduler) from the same Go module — deployed in lockstep, same image tag per release
- Job dispatch is inline (no message bus) — the API check-in handler is the dispatcher; Phase 6 removed NATS for simplicity
