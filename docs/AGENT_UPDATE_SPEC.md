# Agent Update Mechanism Specification

---

## Overview

Agent updates are delivered as `agent_update` jobs via the standard job queue. The agent downloads the new binary, verifies its integrity and authenticity, installs it side-by-side with the running binary, restarts into the new version, and confirms successful startup to the server. If the new binary fails to start, the agent automatically rolls back to the previous version. Operators can also manually trigger rollback for runtime issues discovered after successful startup.

---

## Update Triggering

### Automatic Updates

Auto-update behavior is configurable at the tenant level and overridable per group. When a new agent version is published to the server, the server evaluates each device's auto-update policy and enqueues `agent_update` jobs accordingly.

**Auto-update policy (tenant-level default):**
```json
{
  "auto_update": {
    "enabled": true,
    "channel": "stable",
    "schedule": "cron:0 2 * * 0",
    "rollout": {
      "strategy": "gradual",
      "batch_percent": 10,
      "batch_interval_minutes": 60
    }
  }
}
```

**Policy fields:**

| Field | Description |
|---|---|
| `enabled` | Whether auto-update is active for this tenant/group |
| `channel` | `stable`, `beta`, or `canary` |
| `schedule` | Cron expression defining when updates are applied. Omit for immediate rollout. |
| `rollout.strategy` | `immediate` (all at once) or `gradual` (batched rollout) |
| `rollout.batch_percent` | Percentage of target devices per batch (gradual only) |
| `rollout.batch_interval_minutes` | Time between batches (gradual only) |

**Policy precedence (highest to lowest):**
1. Manual operator job (always takes precedence)
2. Group-level auto-update policy
3. Tenant-level auto-update policy

Groups with no explicit policy inherit the tenant default. Auto-update can be disabled at the group level even if enabled at the tenant level.

### Manual Updates

Operators can trigger an update job at any time via the API or web UI, targeting any combination of devices, groups, tags, or sites. Manual jobs bypass schedule and rollout constraints but still respect CDM.

```
POST /v1/jobs
```

```json
{
  "type": "agent_update",
  "target": {
    "group_ids": ["grp_01HZ..."]
  },
  "payload": {
    "version": "1.5.0",
    "channel": "stable"
  }
}
```

Omitting `version` targets the latest available version on the specified channel.

---

## Agent Version Registry

The server maintains a registry of available agent versions per channel.

### Publish Agent Version
```
POST /v1/agent-versions
Authorization: Bearer <admin_api_key>
```

```json
{
  "version": "1.5.0",
  "channel": "stable",
  "changelog": "Bug fixes and performance improvements.",
  "binaries": [
    {
      "os": "linux",
      "arch": "amd64",
      "file_id": "fil_01HZ...",
      "sha256": "e3b0c44298fc1c149afb...",
      "signature": "<base64-encoded Ed25519 signature>",
      "signature_key_id": "sigkey_01HZ..."
    },
    {
      "os": "linux",
      "arch": "arm64",
      "file_id": "fil_02HZ...",
      "sha256": "...",
      "signature": "...",
      "signature_key_id": "sigkey_01HZ..."
    },
    {
      "os": "windows",
      "arch": "amd64",
      "file_id": "fil_03HZ...",
      "sha256": "...",
      "signature": "...",
      "signature_key_id": "sigkey_01HZ..."
    }
  ]
}
```

### List Agent Versions
```
GET /v1/agent-versions
```
**Query params:** `channel`, `cursor`, `limit`

### Get Agent Version
```
GET /v1/agent-versions/{version}
```

### Yank Agent Version
```
POST /v1/agent-versions/{version}/yank
```
Marks a version as yanked — it will no longer be offered for auto-update and the server will warn if a manual update to this version is attempted. Does not affect devices already running it.

```json
{ "reason": "Critical bug in certificate renewal" }
```

---

## Update Job Payload

When the server dispatches an `agent_update` job, the payload includes everything the agent needs to download and verify the new binary:

```json
{
  "version": "1.5.0",
  "download_url": "https://server/v1/files/fil_01HZ.../download",
  "sha256": "e3b0c44298fc1c149afb...",
  "signature": "<base64-encoded Ed25519 signature>",
  "signature_key_id": "sigkey_01HZ...",
  "size_bytes": 20971520,
  "min_rollback_version": "1.4.0"
}
```

`min_rollback_version` is the oldest version the server considers safe to roll back to. The agent uses this to determine whether its current binary is an acceptable rollback target.

---

## Agent Update Flow

### 1. Pre-flight Checks

Before downloading, the agent verifies:
- Target version is newer than current version (refuses downgrades unless `force: true` in payload)
- Sufficient disk space for the new binary (same free space check as file transfer — configurable threshold)
- Current binary path is known and readable (needed for rollback)

### 2. Download

The agent downloads the new binary in chunks using Range requests, identical to the file transfer mechanism. The binary is written to a staging path alongside the current binary:

| Platform | Staging Path |
|---|---|
| Linux | `/usr/local/bin/agent.new` |
| Windows | `C:\Program Files\Agent\agent.new.exe` |

### 3. Verification

After download completes:

1. **SHA-256 checksum** — computed over the full downloaded binary and compared against `sha256` in the job payload. Mismatch → delete staging binary → job fails.
2. **Ed25519 signature** — verified against the registered public key identified by `signature_key_id`. Invalid signature → delete staging binary → job fails.

Both checks must pass. A binary that passes checksum but fails signature (or vice versa) is rejected.

### 4. Side-by-Side Staging

```
Current state:
  /usr/local/bin/agent          ← running binary (v1.4.2)
  /usr/local/bin/agent.new      ← verified new binary (v1.5.0)
  /usr/local/bin/agent.previous ← previous binary (v1.3.1, if exists)
```

The agent:
1. Copies the current binary to `agent.previous` (overwriting any older `.previous`)
2. Replaces `agent` with `agent.new` atomically (via `rename` / `MoveFileEx`)
3. Deletes `agent.new`

The rename is atomic on both Linux and Windows — there is no window where the binary path is absent.

### 5. Restart

The agent signals its service manager to restart:

| Platform | Mechanism |
|---|---|
| Linux (systemd) | `systemctl restart agent` via D-Bus or exec |
| Windows | Service Control Manager restart via `ChangeServiceConfig2` / SCM restart action |

The agent submits a partial job result before restarting to record that restart was initiated:
```json
{
  "status": "restarting",
  "message": "New binary installed, restarting into v1.5.0"
}
```

### 6. Post-Restart Verification

After restart, the new agent binary:
1. Reads a startup verification file written by the previous process before restart (contains expected version, job ID, and a deadline timestamp)
2. Confirms its own reported version matches the expected version
3. If verification passes: completes the job with `status: completed`, deletes the startup verification file
4. If verification fails or deadline exceeded: triggers automatic rollback (see below)

**Startup verification file:**

| Platform | Path |
|---|---|
| Linux | `/var/lib/agent/pending_update.json` |
| Windows | `C:\ProgramData\Agent\pending_update.json` |

```json
{
  "job_id": "job_01HZ...",
  "expected_version": "1.5.0",
  "previous_version": "1.4.2",
  "deadline": "2026-03-28T12:10:00Z"
}
```

The deadline is set to `now + (3 × poll_interval_seconds)` — enough time for the agent to restart and complete one check-in cycle.

---

## Rollback

### Automatic Rollback (Start Failure)

Triggered when the new binary fails to start or fails post-restart version verification within the deadline.

The agent (running the old binary, restored by the service manager's restart-on-failure policy, or explicitly by the rollback logic):

1. Reads `pending_update.json` to identify the failed update
2. Renames `agent.previous` back to `agent` atomically
3. Restarts into the restored binary
4. On next check-in, reports the rollback to the server:
```json
"status": {
  "agent_version": "1.4.2",
  "last_update_failed": true,
  "last_update_job_id": "job_01HZ...",
  "last_update_error": "version mismatch after restart: expected 1.5.0, got 1.4.2"
}
```
5. Server transitions the update job to `FAILED` and logs the rollback in the audit log
6. Server suppresses auto-update retries for this device until operator review

### Manual Rollback (Runtime Issues)

Operator-initiated via API or web UI after the new agent version has successfully started but is exhibiting runtime issues.

```
POST /v1/devices/{device_id}/rollback
```

```json
{
  "reason": "High CPU usage observed after update to v1.5.0"
}
```

This enqueues a special `agent_rollback` job. The agent:
1. Verifies `agent.previous` exists and its version meets `min_rollback_version`
2. Swaps `agent` and `agent.previous` atomically
3. Restarts into the previous binary
4. Reports success on next check-in

If no `agent.previous` exists or the previous version is below `min_rollback_version`, the rollback job fails with an appropriate error and the operator is notified.

**Rollback is a one-generation operation** — only the immediately previous binary is retained. Rolling back twice in succession is not supported; the operator must push a specific version via a manual update job instead.

---

## Gradual Rollout

When `rollout.strategy` is `gradual`, the server batches update jobs across the target device population:

1. Server selects `batch_percent`% of target devices randomly (seeded per rollout for reproducibility)
2. Enqueues `agent_update` jobs for the first batch
3. Waits `batch_interval_minutes`
4. If no rollback events detected in the batch: proceeds to next batch
5. If rollback rate in the batch exceeds 10% (configurable): **pauses the rollout** and alerts the operator
6. Operator can resume, abort, or adjust the rollout via API

**Rollout management:**

```
GET  /v1/agent-versions/{version}/rollout        ← rollout status
POST /v1/agent-versions/{version}/rollout/pause  ← pause gradual rollout
POST /v1/agent-versions/{version}/rollout/resume ← resume paused rollout
POST /v1/agent-versions/{version}/rollout/abort  ← abort; no further batches
```

Aborting a rollout does not roll back devices that have already updated successfully.

---

## Update Channels

| Channel | Description | Intended audience |
|---|---|---|
| `stable` | Fully tested releases | All production devices |
| `beta` | Release candidates | Opt-in test groups |
| `canary` | Latest builds | Internal / developer devices |

Devices inherit their channel from their group policy, falling back to tenant policy. A device can be pinned to a specific channel independently of its group via a device-level override.

---

## CDM Compliance

`agent_update` jobs are subject to CDM in the same way as all other execution jobs. If a device has CDM enabled with no active session, the update job transitions to `CDM_HOLD` and waits for the local user to grant a session. Auto-update scheduling should account for this — updates scheduled during business hours for CDM-enabled devices may sit in `CDM_HOLD` for extended periods.

Operators should consider scheduling auto-updates outside business hours for CDM-enabled device populations.

---

## Database Additions

```sql
agent_versions (
  id          uuid primary key,
  version     text not null unique,
  channel     text not null,  -- 'stable' | 'beta' | 'canary'
  changelog   text,
  yanked      bool not null default false,
  yank_reason text,
  created_at  timestamptz not null
)

agent_version_binaries (
  id               uuid primary key,
  agent_version_id uuid not null references agent_versions(id),
  os               text not null,
  arch             text not null,
  file_id          uuid not null references files(id),
  sha256           text not null,
  signature        text not null,
  signature_key_id uuid not null references signing_keys(id),
  unique (agent_version_id, os, arch)
)

agent_update_policies (
  id                      uuid primary key,
  tenant_id               uuid not null references tenants(id),
  group_id                uuid references groups(id),  -- null = tenant default
  enabled                 bool not null default true,
  channel                 text not null default 'stable',
  schedule                text,  -- cron expression or null for immediate
  rollout_strategy        text not null default 'gradual',
  rollout_batch_percent   int not null default 10,
  rollout_batch_interval_minutes int not null default 60,
  unique (tenant_id, group_id)
)
```

---

## Security Considerations

- **Signature is mandatory for agent updates** — unlike general file transfers where signature is optional, `agent_update` jobs always require Ed25519 signature verification. A binary that fails signature verification is deleted and never executed.
- **Atomic binary swap** — use of `rename`/`MoveFileEx` ensures there is no window where the agent binary is absent or partially written
- **Previous binary retention** — `agent.previous` is retained until the next successful update, providing a one-generation rollback target without requiring a network download
- **Startup verification deadline** — prevents a failed new binary from blocking the agent indefinitely; the old binary is restored if the deadline passes
- **Gradual rollout with automatic pause** — limits blast radius of a bad release to the first batch before operator intervention
- **Yank mechanism** — allows rapid suppression of a bad version across all tenants before it spreads further
- **CDM compliance** — update jobs cannot be silently pushed to customer devices without consent, even for security updates
