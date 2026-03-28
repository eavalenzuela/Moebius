# Build Specification — Agent Check-In Protocol & Core Design

---

## Agent Check-In Protocol

**Endpoint**
```
POST /v1/agents/checkin
Authorization: Bearer <agent-token>
```

### Request — Agent → Server

```json
{
  "agent_id": "agt_01HZ...",
  "timestamp": "2026-03-28T12:00:00Z",
  "sequence": 4821,
  "status": {
    "uptime_seconds": 86400,
    "cdm_enabled": true,
    "cdm_session_active": false,
    "cdm_session_expires_at": null,
    "agent_version": "1.4.2"
  },
  "inventory_delta": {
    "packages": {
      "added": [],
      "removed": [],
      "updated": []
    }
  }
}
```

**Notes:**
- `sequence` is a monotonically incrementing counter — the server uses this to detect missed check-ins and out-of-order delivery.
- `inventory_delta` is omitted entirely if nothing has changed since the last check-in.
- CDM session state is always reported so the server has an accurate picture of what the agent will currently accept.

### Response — Server → Agent

```json
{
  "timestamp": "2026-03-28T12:00:01Z",
  "jobs": [
    {
      "job_id": "job_01HZ...",
      "type": "exec",
      "payload": {
        "command": "apt-get update",
        "timeout_seconds": 120
      },
      "created_at": "2026-03-28T11:59:00Z"
    },
    {
      "job_id": "job_02HZ...",
      "type": "inventory_full",
      "payload": {}
    }
  ],
  "config": {
    "poll_interval_seconds": 30
  }
}
```

**Notes:**
- `jobs` is an empty array if nothing is pending — the agent always gets a clean response.
- The `config` block allows the server to tune agent behavior without a redeploy (poll interval, log level, etc.).
- Supported job types: `exec`, `package_install`, `package_remove`, `package_update`, `inventory_full`, `file_transfer`, `agent_update`.
- The server only returns jobs the agent will accept given its current CDM state — CDM-blocked jobs stay queued server-side, invisible to the agent until a session is open.

### Inventory Model

- **Full inventory** is collected and shipped on a server-defined schedule (default: every 24h), delivered as an `inventory_full` job.
- **Delta inventory** is included in the check-in request body whenever changes are detected since the last check-in.
- Full inventory can also be requested on-demand by the server at any time via the job queue (e.g. after a package deployment).

---

## Job Lifecycle State Machine

```
PENDING
  │
  ▼
QUEUED ──────────────────────────────► CDM_HOLD
  │                                      │
  │                                      │ (session granted)
  ▼                                      │
DISPATCHED ◄───────────────────────────┘
  │  │
  │  └── (one missed check-in) ──► QUEUED (auto-requeue)
  │
  │ (agent acknowledges)
  ▼
ACKNOWLEDGED
  │
  │ (agent begins execution)
  ▼
RUNNING
  │
  ├──► COMPLETED
  ├──► FAILED ──► (retry policy) ──► QUEUED
  ├──► TIMED_OUT ──► (retry policy) ──► QUEUED
  └──► CANCELLED (server stops dispatching; terminal)
```

### State Definitions

| State | Description |
|---|---|
| `PENDING` | Job created, not yet on the queue (e.g. scheduled job waiting for its trigger time) |
| `QUEUED` | On the job queue, waiting to be dispatched to the agent |
| `CDM_HOLD` | Agent is in CDM with no active session; job waits here until a session opens or the job is cancelled |
| `DISPATCHED` | Included in a check-in response, awaiting agent acknowledgement |
| `ACKNOWLEDGED` | Agent has confirmed receipt; execution imminent |
| `RUNNING` | Agent is actively executing the job |
| `COMPLETED` | Terminal — job completed successfully |
| `FAILED` | Terminal (after retries exhausted) — job failed |
| `TIMED_OUT` | Terminal (after retries exhausted) — job exceeded its timeout |
| `CANCELLED` | Terminal — server stopped dispatching the job; agent never receives it again |

### Retry Policy — Per Job Type Defaults

| Job Type | Max Retries | Retry Delay | Retry On |
|---|---|---|---|
| `exec` | 0 | — | — |
| `package_install` | 3 | 5 min backoff | FAILED, TIMED_OUT |
| `package_remove` | 3 | 5 min backoff | FAILED, TIMED_OUT |
| `package_update` | 3 | 5 min backoff | FAILED, TIMED_OUT |
| `inventory_full` | 5 | 1 min backoff | FAILED, TIMED_OUT |
| `file_transfer` | 3 | 5 min backoff | FAILED, TIMED_OUT |
| `agent_update` | 2 | 10 min backoff | FAILED, TIMED_OUT |

**Notes:**
- Retry policies are defaults — overridable at job creation time.
- Each retry creates a new job record linked to the original via `parent_job_id` — full history is preserved.
- Retry count and last error are stored on the job record.
- Exhausted retries transition the job to `FAILED` permanently; an operator must explicitly retry.

### Cancellation

Cancellation is server-side only. The server stops including the job in check-in responses. The agent is not notified out-of-band. Any in-flight job (in `RUNNING` state) will complete — the agent does not receive a kill signal. `CANCELLED` is a terminal state and is not retried.

### Dispatch Timeout

If an agent goes offline after a job is `DISPATCHED` but before acknowledging, the job automatically requeues to `QUEUED` after one missed check-in (~60s at the default 30s poll interval).

---

## CDM Session Signaling

CDM (Customer Device Mode) state is **local-authoritative** — it lives on the agent. The server mirrors what the agent reports and never assumes state between check-ins.

### Agent → Server

CDM state is always included in the check-in request body. No separate endpoint is required.

```json
"status": {
  "cdm_enabled": true,
  "cdm_session_active": true,
  "cdm_session_expires_at": "2026-03-28T12:10:00Z"
}
```

### Server Behavior Based on CDM State

| Agent CDM State | Server Behavior |
|---|---|
| CDM disabled | Jobs dispatched normally |
| CDM enabled, no session | Jobs transition to `CDM_HOLD`; not dispatched |
| CDM enabled, session active | Jobs dispatched normally within session window |
| Session expiring (< 1 poll interval remaining) | Server dispatches no new jobs; lets in-flight complete |
| Session expired (reported on next check-in) | Any remaining `DISPATCHED` jobs requeue to `CDM_HOLD` |

### What CDM Does Not Block

CDM gates **inbound execution only**. The following always flow regardless of CDM state:
- Heartbeat / check-in
- Inventory collection and delta reporting
- Agent-initiated log shipping
- Local UI and CLI access

---

## Database Schema

### Tenants

```sql
tenants (
  id          uuid primary key,
  name        text not null,
  slug        text not null unique,
  created_at  timestamptz not null,
  config      jsonb
)
```

### Users & Auth

```sql
users (
  id          uuid primary key,
  tenant_id   uuid not null references tenants(id),
  email       text not null,
  role_id     uuid references roles(id),
  sso_subject text,
  created_at  timestamptz not null
)

roles (
  id          uuid primary key,
  tenant_id   uuid references tenants(id),  -- null = system role
  name        text not null,
  permissions jsonb not null,
  is_custom   bool not null default false
)

api_keys (
  id            uuid primary key,
  tenant_id     uuid not null references tenants(id),
  user_id       uuid references users(id),
  name          text not null,
  key_hash      text not null,
  role_id       uuid references roles(id),
  scope         jsonb,           -- null = unrestricted (admin key)
  is_admin      bool not null default false,
  last_used_at  timestamptz,
  expires_at    timestamptz
)
```

### Devices

```sql
devices (
  id                    uuid primary key,
  tenant_id             uuid not null references tenants(id),
  hostname              text not null,
  os                    text not null,
  os_version            text not null,
  arch                  text not null,
  agent_version         text not null,
  last_seen_at          timestamptz,
  registered_at         timestamptz not null,
  cdm_enabled           bool not null default false,
  cdm_session_active    bool not null default false,
  cdm_session_expires_at timestamptz,
  sequence_last         bigint not null default 0
)
```

### Grouping

```sql
groups (
  id        uuid primary key,
  tenant_id uuid not null references tenants(id),
  name      text not null
)

device_groups (
  device_id uuid not null references devices(id),
  group_id  uuid not null references groups(id),
  primary key (device_id, group_id)
)

tags (
  id        uuid primary key,
  tenant_id uuid not null references tenants(id),
  name      text not null
)

device_tags (
  device_id uuid not null references devices(id),
  tag_id    uuid not null references tags(id),
  primary key (device_id, tag_id)
)

sites (
  id        uuid primary key,
  tenant_id uuid not null references tenants(id),
  name      text not null,
  location  text
)

device_sites (
  device_id uuid not null references devices(id),
  site_id   uuid not null references sites(id),
  primary key (device_id, site_id)
)
```

### Inventory

```sql
inventory_hardware (
  id                 uuid primary key,
  device_id          uuid not null references devices(id),
  collected_at       timestamptz not null,
  cpu                jsonb,
  ram_mb             bigint,
  disks              jsonb,
  network_interfaces jsonb
)

inventory_packages (
  id           uuid primary key,
  device_id    uuid not null references devices(id),
  name         text not null,
  version      text not null,
  manager      text not null,
  installed_at timestamptz,
  last_seen_at timestamptz not null
)
```

### Jobs

```sql
jobs (
  id              uuid primary key,
  tenant_id       uuid not null references tenants(id),
  device_id       uuid not null references devices(id),
  parent_job_id   uuid references jobs(id),
  type            text not null,
  status          text not null,
  payload         jsonb not null,
  retry_policy    jsonb,
  retry_count     int not null default 0,
  max_retries     int not null default 0,
  last_error      text,
  created_by      uuid references users(id),
  created_at      timestamptz not null,
  dispatched_at   timestamptz,
  acknowledged_at timestamptz,
  started_at      timestamptz,
  completed_at    timestamptz
)

job_results (
  id           uuid primary key,
  job_id       uuid not null references jobs(id),
  exit_code    int,
  stdout       text,
  stderr       text,
  started_at   timestamptz,
  completed_at timestamptz
)
```

### Scheduling

```sql
scheduled_jobs (
  id            uuid primary key,
  tenant_id     uuid not null references tenants(id),
  name          text not null,
  job_type      text not null,
  payload       jsonb not null,
  target        jsonb not null,  -- device, group, tag, or site
  cron_expr     text not null,
  retry_policy  jsonb,
  enabled       bool not null default true,
  last_run_at   timestamptz,
  next_run_at   timestamptz
)
```

### Audit Log

```sql
audit_log (
  id            uuid primary key,
  tenant_id     uuid not null references tenants(id),
  actor_id      uuid not null,
  actor_type    text not null,  -- 'user' | 'api_key' | 'agent' | 'system'
  action        text not null,
  resource_type text not null,
  resource_id   uuid,
  metadata      jsonb,
  ip_address    text,
  created_at    timestamptz not null
)
```

### Alerts

```sql
alert_rules (
  id        uuid primary key,
  tenant_id uuid not null references tenants(id),
  name      text not null,
  condition jsonb not null,
  channels  jsonb not null,
  enabled   bool not null default true
)
```

---

## Schema Design Notes

- `payload`, `permissions`, `scope`, `target`, and `condition` fields use `jsonb` for flexibility without requiring schema migrations for every new job type, permission, or alert condition.
- `scope` on `api_keys` encodes multi-dimensional scoping (groups, tags, sites, individual devices) in a single column.
- `parent_job_id` on `jobs` links retries to their origin job — full lineage is queryable.
- `target` on `scheduled_jobs` flexibly encodes device, group, tag, or site targets.
- `actor_type` on `audit_log` distinguishes user, api_key, agent, and system-initiated actions.
- Every tenant-scoped table carries `tenant_id` — all queries must filter by it to enforce tenant isolation.
- `tenant_id` on `roles` is nullable — null indicates a system-defined (predefined) role shared across all tenants.
