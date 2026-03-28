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
│  - Job creation → NATS JetStream                     │
│  - Agent check-in + enrollment                       │
│  - File upload handling                              │
│  - Tenant management                                 │
└──────┬─────────────────────┬───────────────────────┘
       │                     │
┌──────▼──────┐     ┌────────▼─────────────────────────┐
│  PostgreSQL  │     │        NATS JetStream             │
│  - devices   │     │  Streams:                         │
│  - inventory │     │  - jobs (dispatch, work queue)    │
│  - jobs      │     │  - results (interest-based)       │
│  - audit log │     │  - logs (max age 7d)              │
│  - RBAC      │     └──┬─────────────────────┬─────────┘
│  - tenants   │        │                     │
└──────▲───────┘  ┌─────▼──────┐    ┌─────────▼──────────┐
       │          │  Worker(s)  │    │     Scheduler       │
       │          │  (Go)       │    │  (Go, single-active │
       └──────────│  stateless, │    │   via PG advisory   │
                  │  scalable   │    │   lock)             │
                  └─────┬──────┘    └──────────────────────┘
                        │ HTTPS polling
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
Publishes jobs to NATS JetStream; reads results from PostgreSQL.
Serves agent check-in and enrollment endpoints.
Returns pending jobs to agents on check-in, filtered by CDM state.
Handles chunked file uploads and download URL generation.
Subcommands: `migrate`, `generate-ca`, `create-admin`.

#### NATS JetStream

Decouples job creation from job execution.
Allows multiple workers to scale independently.
Three streams:
- `jobs` — work queue semantics (deleted on ack), subjects: `jobs.dispatch.{tenant_id}.{device_id}`
- `results` — interest-based retention, subjects: `results.{tenant_id}.{job_id}`
- `logs` — agent log shipping, max age 7 days, subjects: `logs.{tenant_id}.{device_id}`

#### Worker (`server/cmd/worker`)

Consumes jobs from NATS JetStream `jobs` stream.
Manages the job state machine (QUEUED → DISPATCHED → ACKNOWLEDGED → RUNNING → terminal).
Handles CDM hold logic: if agent reports CDM enabled with no session, jobs transition to CDM_HOLD.
Writes job state transitions and results to PostgreSQL.
Stateless — run as many replicas as needed; NATS handles work distribution.
Each instance processes jobs concurrently with a configurable goroutine pool.

#### Scheduler (`server/cmd/scheduler`)

Single active instance via PostgreSQL advisory lock (leader election); a second replica can stand by for failover.
Evaluates cron expressions for scheduled jobs and enqueues them when due.
Evaluates auto-update policies when new agent versions are published.
Manages gradual rollout batching for agent updates.
Monitors device last-seen timestamps and fires alert rules.
Sends webhook and email notifications.

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
│   │   ├── worker/      # worker binary
│   │   └── scheduler/   # scheduler binary
│   ├── api/             # REST handlers
│   ├── auth/            # API key + OIDC + mTLS
│   ├── rbac/            # role + scope enforcement
│   ├── jobs/            # job lifecycle management
│   ├── worker/          # job queue consumers
│   ├── store/           # PostgreSQL data layer
│   └── notify/          # alerting + webhooks
├── shared/
│   ├── protocol/        # agent<->server request/response types
│   ├── models/          # shared domain types
│   └── version/         # build-time version injection
├── ui/                  # React frontend
├── cli/                 # Admin CLI (server-side)
├── deploy/
│   ├── docker/          # Dockerfiles for api, worker, scheduler
│   ├── docker-compose.yml
│   ├── helm/            # Kubernetes Helm chart for SaaS deployment
│   └── migrations/      # PostgreSQL schema migrations (forward-only)
├── keys/                # release.pub (Ed25519 public key for artifact signing)
└── .github/workflows/   # CI (lint, test, build) + release pipeline
```

### Key Design Principles

- API server is the only component with a public network surface — workers, scheduler, DB, and queue are internal only
- Agents never receive inbound connections — poll-only means no open ports on endpoints
- CDM is enforced on the agent, not the server — server cannot bypass it even if compromised
- All RBAC enforcement is in the API server — workers trust that jobs have already been authorized
- Tenant isolation at the DB layer — every table with tenant-scoped data carries a `tenant_id` and queries always filter by it
- Server is three processes (API, worker, scheduler) from the same Go module — deployed in lockstep, same image tag per release
