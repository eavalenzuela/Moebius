# User Guide

This guide covers day-to-day operation of Moebius from an operator's perspective. For infrastructure setup, see [Deployment Instructions](Deployment_Instructions.md). For the full API reference, see [REST API Spec](REST_API_SPEC.md).

All examples use `curl`. Replace `$SERVER` with your server URL and `$API_KEY` with your API key.

```bash
export SERVER=https://manage.example.com
export API_KEY=sk_...
```

---

## Table of Contents

- [Enrolling Agents](#enrolling-agents)
- [Managing Devices](#managing-devices)
- [Running Jobs](#running-jobs)
- [File Transfer](#file-transfer)
- [Customer Device Mode (CDM)](#customer-device-mode-cdm)
- [Scheduled Jobs](#scheduled-jobs)
- [Agent Updates](#agent-updates)
- [Alert Rules](#alert-rules)
- [User Management & RBAC](#user-management--rbac)
- [API Keys](#api-keys)
- [Audit Log](#audit-log)

---

## Enrolling Agents

### 1. Create an Enrollment Token

```bash
curl -X POST $SERVER/v1/enrollment-tokens \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{}'
```

The response includes a `token` field — this is shown once and cannot be retrieved later.

Tokens are single-use and expire after 24 hours by default. You can scope tokens to automatically assign enrolled devices to groups, tags, or sites:

```bash
curl -X POST $SERVER/v1/enrollment-tokens \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "scope": {
      "group_ids": ["grp_abc123"],
      "tag_ids": ["tag_def456"]
    }
  }'
```

### 2. Install the Agent

**Linux:**

```bash
sudo ./install.sh \
  --enrollment-token <token> \
  --server-url $SERVER
```

The install script creates a system user, installs the binary, writes the config, and starts the systemd service. The agent enrolls automatically, receives its mTLS certificate, and begins polling.

**Windows:**

```powershell
.\moebius-agent.exe install --enrollment-token <token> --server-url $SERVER
```

### 3. Verify Enrollment

```bash
curl $SERVER/v1/devices \
  -H "Authorization: Bearer $API_KEY" | jq '.data[] | {id, hostname, status}'
```

The device should appear with `status: "online"` within 30 seconds (one poll interval).

---

## Managing Devices

### List Devices

```bash
curl "$SERVER/v1/devices" \
  -H "Authorization: Bearer $API_KEY"
```

### View Device Details

```bash
curl "$SERVER/v1/devices/$DEVICE_ID" \
  -H "Authorization: Bearer $API_KEY"
```

### Update Device Hostname

```bash
curl -X PATCH "$SERVER/v1/devices/$DEVICE_ID" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"hostname": "new-hostname"}'
```

### Revoke a Device

Revoking a device prevents it from checking in. The agent must re-enroll with a new token.

```bash
curl -X POST "$SERVER/v1/devices/$DEVICE_ID/revoke" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"reason": "decommissioned"}'
```

### Organizing Devices

Devices can be organized into **groups**, **tags**, and **sites**.

**Create a group:**

```bash
curl -X POST "$SERVER/v1/groups" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "Production Servers"}'
```

**Add a device to a group:**

```bash
curl -X POST "$SERVER/v1/groups/$GROUP_ID/devices" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"device_ids": ["dev_abc123"]}'
```

Tags and sites work the same way (`/v1/tags`, `/v1/sites`).

### View Device Inventory

```bash
curl "$SERVER/v1/devices/$DEVICE_ID/inventory" \
  -H "Authorization: Bearer $API_KEY"
```

Returns hardware info and installed packages as reported by the agent.

---

## Running Jobs

Jobs are the primary way to take action on managed devices. Each job targets one or more devices and contains a typed payload.

### Job Types

| Type | Description |
|------|-------------|
| `exec` | Run a shell command |
| `package_install` | Install a package |
| `package_remove` | Remove a package |
| `package_update` | Update a package |
| `inventory_full` | Request full inventory collection |
| `file_transfer` | Transfer a file to the device |
| `agent_update` | Update the agent binary |

### Run a Command

```bash
curl -X POST "$SERVER/v1/jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "exec",
    "target": {"device_ids": ["dev_abc123"]},
    "payload": {
      "command": "uptime",
      "timeout_seconds": 30
    }
  }'
```

### Target Multiple Devices

Jobs can target devices by ID, group, tag, or site. The server resolves these to individual device IDs and creates one job per device.

```bash
curl -X POST "$SERVER/v1/jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "exec",
    "target": {"group_ids": ["grp_abc123"]},
    "payload": {"command": "apt update && apt upgrade -y"}
  }'
```

### Install a Package

```bash
curl -X POST "$SERVER/v1/jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "package_install",
    "target": {"device_ids": ["dev_abc123"]},
    "payload": {
      "name": "nginx",
      "version": "1.24.0",
      "manager": "apt"
    }
  }'
```

### Retry Policy

Jobs can include a retry policy. If a job fails, the server automatically creates a new linked job:

```bash
curl -X POST "$SERVER/v1/jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "exec",
    "target": {"device_ids": ["dev_abc123"]},
    "payload": {"command": "systemctl restart myapp"},
    "retry_policy": {
      "max_retries": 3,
      "retry_delay_seconds": 60
    }
  }'
```

### Monitor Job Status

```bash
# List all jobs
curl "$SERVER/v1/jobs" -H "Authorization: Bearer $API_KEY"

# Get a specific job (includes result if completed)
curl "$SERVER/v1/jobs/$JOB_ID" -H "Authorization: Bearer $API_KEY"
```

### Job Lifecycle

```
QUEUED → DISPATCHED → ACKNOWLEDGED → RUNNING → COMPLETED
                                             → FAILED (may auto-retry)
                                             → TIMED_OUT
QUEUED → CANCELLED (if cancelled before dispatch)
QUEUED → CDM_HOLD (if CDM is active without a session)
```

### Cancel a Job

```bash
curl -X POST "$SERVER/v1/jobs/$JOB_ID/cancel" \
  -H "Authorization: Bearer $API_KEY"
```

Only jobs in a non-terminal state can be cancelled.

### Retry a Failed Job

```bash
curl -X POST "$SERVER/v1/jobs/$JOB_ID/retry" \
  -H "Authorization: Bearer $API_KEY"
```

---

## File Transfer

Files can be uploaded to the server and then transferred to agents via jobs.

### 1. Upload a File

**Initiate:**

```bash
SHA=$(sha256sum myfile.tar.gz | awk '{print $1}')
SIZE=$(stat -c%s myfile.tar.gz)

curl -X POST "$SERVER/v1/files" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"filename\": \"myfile.tar.gz\",
    \"size_bytes\": $SIZE,
    \"sha256\": \"$SHA\"
  }"
```

**Upload chunks:**

```bash
curl -X PUT "$SERVER/v1/files/uploads/$UPLOAD_ID/chunks/0" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @myfile.tar.gz
```

**Complete:**

```bash
curl -X POST "$SERVER/v1/files/uploads/$UPLOAD_ID/complete" \
  -H "Authorization: Bearer $API_KEY"
```

### 2. Transfer to Devices

```bash
curl -X POST "$SERVER/v1/jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "file_transfer",
    "target": {"device_ids": ["dev_abc123"]},
    "payload": {
      "file_id": "fil_xyz789",
      "integrity": {"require_sha256": true},
      "on_complete": {"exec": "tar xzf /var/lib/moebius-agent/drop/myfile.tar.gz -C /opt/app"}
    }
  }'
```

Files are downloaded to `/var/lib/moebius-agent/drop/` on the agent. The optional `on_complete.exec` command runs after successful download and verification.

---

## Customer Device Mode (CDM)

CDM gives end users control over when management jobs execute on their device. When enabled, all incoming jobs are held until the user grants a time-limited session.

CDM is managed locally on the agent, not from the server.

### Enable CDM

On the managed device:

```bash
moebius-agent cdm enable
```

### Grant a Session

```bash
# Allow jobs to execute for the next 2 hours
moebius-agent cdm grant --duration 2h
```

While a session is active, the agent reports `cdm_session_active: true` in check-ins, and the server dispatches held jobs.

### Revoke a Session

```bash
moebius-agent cdm revoke
```

New jobs are immediately held again. Jobs already dispatched continue executing.

### Check CDM Status

```bash
moebius-agent cdm status
```

### Server Behavior

- **CDM enabled, no session**: Server moves QUEUED jobs to CDM_HOLD
- **CDM enabled, session active**: Server releases CDM_HOLD jobs back to QUEUED and dispatches them
- **CDM disabled**: Jobs dispatch normally

The CDM state is visible in the device details via the API (`cdm_enabled`, `cdm_session_active`, `cdm_session_expires_at`).

---

## Scheduled Jobs

Scheduled jobs run on a cron schedule and automatically create jobs for the specified targets.

### Create a Scheduled Job

```bash
curl -X POST "$SERVER/v1/scheduled-jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Daily package update",
    "job_type": "exec",
    "cron_expr": "0 2 * * *",
    "target": {"group_ids": ["grp_abc123"]},
    "payload": {"command": "apt update && apt upgrade -y"},
    "enabled": true
  }'
```

### Manage Scheduled Jobs

```bash
# List
curl "$SERVER/v1/scheduled-jobs" -H "Authorization: Bearer $API_KEY"

# Enable/disable
curl -X POST "$SERVER/v1/scheduled-jobs/$ID/enable" -H "Authorization: Bearer $API_KEY"
curl -X POST "$SERVER/v1/scheduled-jobs/$ID/disable" -H "Authorization: Bearer $API_KEY"

# Delete
curl -X DELETE "$SERVER/v1/scheduled-jobs/$ID" -H "Authorization: Bearer $API_KEY"
```

---

## Agent Updates

### Publish a New Version

Register a new agent version on the server:

```bash
curl -X POST "$SERVER/v1/agent-versions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "version": "2.0.0",
    "channel": "stable"
  }'
```

### Trigger an Update

Create an `agent_update` job targeting specific devices:

```bash
curl -X POST "$SERVER/v1/jobs" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "agent_update",
    "target": {"device_ids": ["dev_abc123"]},
    "payload": {
      "version": "2.0.0",
      "download_url": "https://manage.example.com/v1/installers/linux/amd64/2.0.0",
      "sha256": "<checksum>",
      "signature": "<base64 signature>",
      "signature_key_id": "sgk_abc123",
      "size_bytes": 15000000
    }
  }'
```

The agent downloads the new binary, verifies the checksum and signature, stages it, and restarts. If the new version fails post-restart verification, the agent automatically rolls back and reports the failure.

### Yank a Version

If a version has issues, yank it to prevent further rollouts:

```bash
curl -X POST "$SERVER/v1/agent-versions/2.0.0/yank" \
  -H "Authorization: Bearer $API_KEY"
```

---

## Alert Rules

Alert rules monitor device conditions and send notifications when triggered.

### Create an Alert Rule

```bash
curl -X POST "$SERVER/v1/alert-rules" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Device offline > 5 minutes",
    "condition": "device_offline",
    "threshold_minutes": 5,
    "notify": {
      "email": ["ops@example.com"],
      "webhook": "https://hooks.slack.com/..."
    },
    "enabled": true
  }'
```

### Manage Alert Rules

```bash
# List
curl "$SERVER/v1/alert-rules" -H "Authorization: Bearer $API_KEY"

# Enable/disable
curl -X POST "$SERVER/v1/alert-rules/$RULE_ID/enable" -H "Authorization: Bearer $API_KEY"
curl -X POST "$SERVER/v1/alert-rules/$RULE_ID/disable" -H "Authorization: Bearer $API_KEY"

# Delete
curl -X DELETE "$SERVER/v1/alert-rules/$RULE_ID" -H "Authorization: Bearer $API_KEY"
```

---

## User Management & RBAC

### Predefined Roles

| Role | Can do |
|------|--------|
| **Super Admin** | Everything |
| **Tenant Admin** | Everything except cross-tenant operations |
| **Operator** | Manage devices, jobs, files, alerts, enrollment — not users or roles |
| **Technician** | Read devices, create jobs, deploy packages — no device writes or admin |
| **Viewer** | Read-only: devices, jobs, inventory, groups, tags, sites |

### Invite a User

```bash
curl -X POST "$SERVER/v1/users/invite" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "new.user@example.com",
    "role_id": "rol_abc123"
  }'
```

### Create a Custom Role

```bash
curl -X POST "$SERVER/v1/roles" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Deploy Only",
    "permissions": ["devices:read", "jobs:read", "jobs:create", "packages:deploy"]
  }'
```

### List Available Permissions

See the [REST API Spec](REST_API_SPEC.md) for the full list. Common permissions:

| Permission | Description |
|------------|-------------|
| `devices:read` | List and view devices |
| `devices:write` | Update device properties |
| `devices:revoke` | Revoke devices |
| `jobs:read` | List and view jobs |
| `jobs:create` | Create new jobs |
| `jobs:retry` | Retry failed jobs |
| `groups:read/write` | Manage groups |
| `tags:read/write` | Manage tags |
| `users:read/write` | Manage users |
| `roles:read/write` | Manage roles |
| `api_keys:read/write` | Manage API keys |
| `audit_log:read` | View audit log |

---

## API Keys

### Create an API Key

```bash
curl -X POST "$SERVER/v1/api-keys" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI/CD automation",
    "role_id": "rol_abc123",
    "scope": {
      "group_ids": ["grp_production"]
    }
  }'
```

The raw key (`sk_...`) is returned once and cannot be retrieved later.

### List API Keys

```bash
curl "$SERVER/v1/api-keys" -H "Authorization: Bearer $API_KEY"
```

### Revoke an API Key

```bash
curl -X DELETE "$SERVER/v1/api-keys/$KEY_ID" \
  -H "Authorization: Bearer $API_KEY"
```

---

## Audit Log

All administrative actions are recorded in the audit log.

```bash
curl "$SERVER/v1/audit-log" -H "Authorization: Bearer $API_KEY"
```

Each entry includes:
- **actor_id** and **actor_type** — who performed the action
- **action** — what was done (e.g., `device.enroll`, `job.create`, `cert.renew`)
- **resource_type** and **resource_id** — what was affected
- **timestamp**
- **metadata** — additional context (varies by action)

The audit log is append-only and cannot be modified or deleted through the API.
