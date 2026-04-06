# REST API Specification

---

## General Conventions

### Base URL
```
https://<server>/v1/
```

### Authentication
All endpoints (except agent enrollment and health check) require one of:
- `Authorization: Bearer <api_key>` — for user and integration access
- mTLS client certificate — for agent endpoints

### Pagination
All list endpoints use cursor-based pagination.

**Request:**
```
GET /v1/devices?limit=50&cursor=<opaque_cursor>
```

**Response envelope:**
```json
{
  "data": [...],
  "pagination": {
    "next_cursor": "eyJpZCI6Ijk...",
    "has_more": true,
    "limit": 50
  }
}
```

- `cursor` is opaque and stable — safe to bookmark and resume
- Default `limit` is 50; maximum is 500
- Omit `cursor` to start from the beginning

### Error Responses
```json
{
  "error": {
    "code": "device_not_found",
    "message": "No device with the given ID exists in this tenant.",
    "request_id": "req_01HZ..."
  }
}
```

### Common Headers
| Header | Description |
|---|---|
| `X-Request-ID` | Echoed from request if provided; generated if not |
| `X-RateLimit-Limit` | Request limit for the current window |
| `X-RateLimit-Remaining` | Remaining requests in the current window |
| `X-RateLimit-Reset` | Unix timestamp when the window resets |

---

## Agent Endpoints

These endpoints are called by agents, authenticated via mTLS client certificate.

### Enroll Agent
```
POST /v1/agents/enroll
```
Unauthenticated. Enrollment-token-gated. See `AGENT_AUTH_SPEC.md` for full flow.

**Request:**
```json
{
  "enrollment_token": "enr_01HZ...",
  "csr": "<PEM-encoded CSR>",
  "hostname": "workstation-42",
  "os": "linux",
  "os_version": "Ubuntu 24.04",
  "arch": "amd64",
  "agent_version": "1.4.2"
}
```

**Response `201`:**
```json
{
  "agent_id": "agt_01HZ...",
  "certificate": "<PEM-encoded signed cert>",
  "ca_chain": "<PEM-encoded intermediate + root>",
  "poll_interval_seconds": 30
}
```

---

### Check In
```
POST /v1/agents/checkin
```
mTLS required. Called every `poll_interval_seconds` by the agent.

**Request:**
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

**Response `200`:**
```json
{
  "timestamp": "2026-03-28T12:00:01Z",
  "jobs": [],
  "config": {
    "poll_interval_seconds": 30
  }
}
```

---

### Renew Certificate
```
POST /v1/agents/renew
```
mTLS required. Called when cert expiry is within 30 days.

**Request:**
```json
{
  "csr": "<PEM-encoded CSR>"
}
```

**Response `200`:**
```json
{
  "certificate": "<PEM-encoded signed cert>",
  "ca_chain": "<PEM-encoded intermediate + root>"
}
```

---

### Submit Job Result
```
POST /v1/agents/jobs/{job_id}/result
```
mTLS required.

**Request:**
```json
{
  "status": "completed",
  "exit_code": 0,
  "stdout": "...",
  "stderr": "",
  "started_at": "2026-03-28T12:00:05Z",
  "completed_at": "2026-03-28T12:00:07Z"
}
```

**Response `204`:** No content.

---

### Acknowledge Job
```
POST /v1/agents/jobs/{job_id}/acknowledge
```
mTLS required. Called immediately when agent receives a job, before execution begins.

**Response `204`:** No content.

---

### Ship Logs
```
POST /v1/agents/logs
```
mTLS required.

**Request:**
```json
{
  "agent_id": "agt_01HZ...",
  "entries": [
    {
      "timestamp": "2026-03-28T12:00:00Z",
      "level": "info",
      "message": "Job job_01HZ... completed successfully"
    }
  ]
}
```

**Response `204`:** No content.

---

## Devices

### List Devices
```
GET /v1/devices
```
**Query params:** `cursor`, `limit`, `status` (online|offline), `group_id`, `tag_id`, `site_id`, `os`, `search`

**Response `200`:**
```json
{
  "data": [
    {
      "id": "dev_01HZ...",
      "tenant_id": "ten_01HZ...",
      "hostname": "workstation-42",
      "os": "linux",
      "os_version": "Ubuntu 24.04",
      "arch": "amd64",
      "agent_version": "1.4.2",
      "status": "online",
      "last_seen_at": "2026-03-28T12:00:00Z",
      "registered_at": "2026-01-01T00:00:00Z",
      "cdm_enabled": true,
      "cdm_session_active": false,
      "cdm_session_expires_at": null,
      "groups": [],
      "tags": [],
      "sites": []
    }
  ],
  "pagination": { "next_cursor": "...", "has_more": false, "limit": 50 }
}
```

---

### Get Device
```
GET /v1/devices/{device_id}
```
**Response `200`:** Single device object (same shape as list item, with full detail).

---

### Update Device
```
PATCH /v1/devices/{device_id}
```
Operator-initiated metadata updates (hostname override, notes, etc.).

**Request:**
```json
{
  "hostname": "workstation-42-renamed"
}
```

**Response `200`:** Updated device object.

---

### Revoke Device
```
POST /v1/devices/{device_id}/revoke
```
Revokes agent certificate, cancels pending jobs, marks device as revoked.

**Request:**
```json
{
  "reason": "Device decommissioned"
}
```

**Response `204`:** No content.

---

### Get Device Inventory
```
GET /v1/devices/{device_id}/inventory
```
**Response `200`:**
```json
{
  "hardware": {
    "cpu": { "model": "...", "cores": 8, "threads": 16 },
    "ram_mb": 16384,
    "disks": [],
    "network_interfaces": []
  },
  "packages": [],
  "collected_at": "2026-03-28T00:00:00Z"
}
```

---

### List Device Jobs
```
GET /v1/devices/{device_id}/jobs
```
**Query params:** `cursor`, `limit`, `status`, `type`

**Response `200`:** Paginated list of job objects.

---

### List Device Logs
```
GET /v1/devices/{device_id}/logs
```
**Query params:** `cursor`, `limit`, `level`, `since`, `until`

**Response `200`:** Paginated list of log entries.

---

## Jobs

### Create Job
```
POST /v1/jobs
```
**Request:**
```json
{
  "type": "exec",
  "target": {
    "device_ids": ["dev_01HZ..."],
    "group_ids": [],
    "tag_ids": [],
    "site_ids": []
  },
  "payload": {
    "command": "apt-get update",
    "timeout_seconds": 120
  },
  "retry_policy": {
    "max_retries": 0
  }
}
```

**Response `201`:**
```json
{
  "job_ids": ["job_01HZ..."],
  "target_device_count": 1
}
```

Note: One job record is created per target device. The response returns all created job IDs.

---

### Get Job
```
GET /v1/jobs/{job_id}
```
**Response `200`:**
```json
{
  "id": "job_01HZ...",
  "device_id": "dev_01HZ...",
  "parent_job_id": null,
  "type": "exec",
  "status": "completed",
  "payload": { "command": "apt-get update", "timeout_seconds": 120 },
  "retry_policy": { "max_retries": 0 },
  "retry_count": 0,
  "last_error": null,
  "created_by": "usr_01HZ...",
  "created_at": "2026-03-28T11:59:00Z",
  "dispatched_at": "2026-03-28T12:00:00Z",
  "acknowledged_at": "2026-03-28T12:00:01Z",
  "started_at": "2026-03-28T12:00:01Z",
  "completed_at": "2026-03-28T12:00:03Z",
  "result": {
    "exit_code": 0,
    "stdout": "...",
    "stderr": ""
  }
}
```

---

### Cancel Job
```
POST /v1/jobs/{job_id}/cancel
```
Valid for jobs in `PENDING`, `QUEUED`, `CDM_HOLD`, or `DISPATCHED` states.

**Response `204`:** No content.

---

### Retry Job
```
POST /v1/jobs/{job_id}/retry
```
Valid for jobs in `FAILED` or `TIMED_OUT` terminal states. Creates a new job linked via `parent_job_id`.

**Response `201`:**
```json
{
  "job_id": "job_02HZ..."
}
```

---

### List Jobs
```
GET /v1/jobs
```
**Query params:** `cursor`, `limit`, `status`, `type`, `device_id`, `created_by`, `since`, `until`

**Response `200`:** Paginated list of job objects.

---

## Scheduled Jobs

### List Scheduled Jobs
```
GET /v1/scheduled-jobs
```
**Response `200`:** Paginated list of scheduled job objects.

---

### Create Scheduled Job
```
POST /v1/scheduled-jobs
```
**Request:**
```json
{
  "name": "Daily inventory refresh",
  "job_type": "inventory_full",
  "payload": {},
  "target": {
    "group_ids": ["grp_01HZ..."]
  },
  "cron_expr": "0 2 * * *",
  "retry_policy": {
    "max_retries": 5,
    "retry_delay_seconds": 60
  },
  "enabled": true
}
```

**Response `201`:** Created scheduled job object.

---

### Get Scheduled Job
```
GET /v1/scheduled-jobs/{scheduled_job_id}
```
**Response `200`:** Scheduled job object.

---

### Update Scheduled Job
```
PATCH /v1/scheduled-jobs/{scheduled_job_id}
```
**Request:** Partial update — any fields from create body.

**Response `200`:** Updated scheduled job object.

---

### Delete Scheduled Job
```
DELETE /v1/scheduled-jobs/{scheduled_job_id}
```
**Response `204`:** No content.

---

### Toggle Scheduled Job
```
POST /v1/scheduled-jobs/{scheduled_job_id}/enable
POST /v1/scheduled-jobs/{scheduled_job_id}/disable
```
**Response `204`:** No content.

---

## Groups

### List Groups
```
GET /v1/groups
```
**Response `200`:** Paginated list of group objects.

---

### Create Group
```
POST /v1/groups
```
**Request:**
```json
{ "name": "Workstations" }
```
**Response `201`:** Created group object.

---

### Get Group
```
GET /v1/groups/{group_id}
```
**Response `200`:** Group object with member count.

---

### Update Group
```
PATCH /v1/groups/{group_id}
```
**Response `200`:** Updated group object.

---

### Delete Group
```
DELETE /v1/groups/{group_id}
```
**Response `204`:** No content.

---

### List Group Members
```
GET /v1/groups/{group_id}/devices
```
**Response `200`:** Paginated list of device objects.

---

### Add Device to Group
```
POST /v1/groups/{group_id}/devices
```
**Request:**
```json
{ "device_ids": ["dev_01HZ..."] }
```
**Response `204`:** No content.

---

### Remove Device from Group
```
DELETE /v1/groups/{group_id}/devices/{device_id}
```
**Response `204`:** No content.

---

## Tags

### List Tags
```
GET /v1/tags
```

### Create Tag
```
POST /v1/tags
```
**Request:** `{ "name": "production" }`

### Delete Tag
```
DELETE /v1/tags/{tag_id}
```

### Add Tag to Device
```
POST /v1/devices/{device_id}/tags
```
**Request:** `{ "tag_ids": ["tag_01HZ..."] }`

### Remove Tag from Device
```
DELETE /v1/devices/{device_id}/tags/{tag_id}
```

---

## Sites

### List Sites
```
GET /v1/sites
```

### Create Site
```
POST /v1/sites
```
**Request:**
```json
{ "name": "HQ London", "location": "London, UK" }
```

### Get Site
```
GET /v1/sites/{site_id}
```

### Update Site
```
PATCH /v1/sites/{site_id}
```

### Delete Site
```
DELETE /v1/sites/{site_id}
```

### List Site Devices
```
GET /v1/sites/{site_id}/devices
```

### Add Device to Site
```
POST /v1/sites/{site_id}/devices
```
**Request:** `{ "device_ids": ["dev_01HZ..."] }`

### Remove Device from Site
```
DELETE /v1/sites/{site_id}/devices/{device_id}
```

---

## Enrollment Tokens

### List Enrollment Tokens
```
GET /v1/enrollment-tokens
```
**Response `200`:** Paginated list — token values are never returned after creation, only metadata.

---

### Create Enrollment Token
```
POST /v1/enrollment-tokens
```
**Request:**
```json
{
  "expires_in_seconds": 86400,
  "scope": {
    "group_ids": ["grp_01HZ..."],
    "site_ids": [],
    "tag_ids": []
  }
}
```

**Response `201`:**
```json
{
  "id": "enr_01HZ...",
  "token": "enr_01HZ_<secret>",
  "expires_at": "2026-03-29T12:00:00Z",
  "scope": { "group_ids": ["grp_01HZ..."] }
}
```

Note: `token` is returned **once only** at creation time. It is stored hashed server-side and cannot be retrieved again.

---

### Revoke Enrollment Token
```
DELETE /v1/enrollment-tokens/{token_id}
```
**Response `204`:** No content.

---

## Users

### List Users
```
GET /v1/users
```
**Response `200`:** Paginated list of user objects (no password fields).

---

### Invite User
```
POST /v1/users/invite
```
**Request:**
```json
{
  "email": "user@example.com",
  "role_id": "rol_01HZ..."
}
```
**Response `201`:** User object with `status: invited`.

---

### Get User
```
GET /v1/users/{user_id}
```

### Update User
```
PATCH /v1/users/{user_id}
```
**Request:** `{ "role_id": "rol_01HZ..." }`

### Deactivate User
```
POST /v1/users/{user_id}/deactivate
```
**Response `204`:** No content.

---

## Roles

### List Roles
```
GET /v1/roles
```
Returns both system (predefined) and tenant custom roles.

---

### Create Custom Role
```
POST /v1/roles
```
**Request:**
```json
{
  "name": "Field Technician",
  "permissions": [
    "devices:read",
    "jobs:create",
    "packages:deploy",
    "inventory:read"
  ]
}
```
**Response `201`:** Created role object.

---

### Get Role
```
GET /v1/roles/{role_id}
```

### Update Custom Role
```
PATCH /v1/roles/{role_id}
```
System roles cannot be modified.

### Delete Custom Role
```
DELETE /v1/roles/{role_id}
```
System roles cannot be deleted. Fails if role is currently assigned to users or API keys.

---

## API Keys

### List API Keys
```
GET /v1/api-keys
```
**Response `200`:** Paginated list — key values are never returned after creation.

---

### Create API Key
```
POST /v1/api-keys
```
**Request:**
```json
{
  "name": "CI Integration",
  "is_admin": false,
  "role_id": "rol_01HZ...",
  "scope": {
    "group_ids": ["grp_01HZ..."]
  },
  "expires_at": "2027-01-01T00:00:00Z"
}
```

**Response `201`:**
```json
{
  "id": "key_01HZ...",
  "name": "CI Integration",
  "key": "sk_01HZ_<secret>",
  "is_admin": false,
  "role_id": "rol_01HZ...",
  "scope": { "group_ids": ["grp_01HZ..."] },
  "expires_at": "2027-01-01T00:00:00Z",
  "created_at": "2026-03-28T12:00:00Z"
}
```

Note: `key` is returned **once only** at creation time.

### Scope Enforcement

When an API key has a non-null `scope`, all requests made with that key are restricted to resources within the scope. Scope fields are unioned to produce the set of allowed device IDs:

- `group_ids` → devices in those groups
- `tag_ids` → devices with those tags
- `site_ids` → devices at those sites
- `device_ids` → those specific devices

**Scope behavior by endpoint:**

| Endpoint category | Behavior |
|---|---|
| Devices (list, get, update, revoke) | Filtered/gated to in-scope devices. Out-of-scope returns 404. |
| Jobs (create) | Target devices intersected with scope. 403 if no overlap. |
| Jobs (list, get, cancel, retry) | Filtered/gated to jobs on in-scope devices. |
| Inventory | Gated to in-scope devices. |
| Groups / Tags / Sites | List filtered to scoped IDs. CRUD gated. Create blocked for scoped keys. |
| Scheduled jobs (create) | Target must overlap with scope. |
| Enrollment tokens (create) | Token scope must be a subset of key scope. |
| Files | Tenant-wide, not scope-restricted. |
| Alert rules | Tenant-wide, not scope-restricted. |
| Users / Roles / API keys / Tenant | Not scope-restricted (RBAC permissions control access). |
| Audit log | Tenant-wide, not scope-restricted. |

Keys with `is_admin=true` bypass all scope checks.

---

### Revoke API Key
```
DELETE /v1/api-keys/{key_id}
```
**Response `204`:** No content.

---

## Alerts

### List Alert Rules
```
GET /v1/alert-rules
```

### Create Alert Rule
```
POST /v1/alert-rules
```
**Request:**
```json
{
  "name": "Agent offline",
  "condition": {
    "type": "agent_offline",
    "threshold_minutes": 5,
    "scope": { "group_ids": ["grp_01HZ..."] }
  },
  "channels": {
    "webhooks": ["https://hooks.example.com/..."],
    "emails": ["ops@example.com"]
  },
  "enabled": true
}
```
**Response `201`:** Created alert rule object.

---

### Get Alert Rule
```
GET /v1/alert-rules/{rule_id}
```

### Update Alert Rule
```
PATCH /v1/alert-rules/{rule_id}
```

### Delete Alert Rule
```
DELETE /v1/alert-rules/{rule_id}
```

### Toggle Alert Rule
```
POST /v1/alert-rules/{rule_id}/enable
POST /v1/alert-rules/{rule_id}/disable
```

---

## Audit Log

### List Audit Log
```
GET /v1/audit-log
```
**Query params:** `cursor`, `limit`, `actor_id`, `actor_type`, `action`, `resource_type`, `resource_id`, `since`, `until`

**Response `200`:**
```json
{
  "data": [
    {
      "id": "aud_01HZ...",
      "actor_id": "usr_01HZ...",
      "actor_type": "user",
      "action": "job.create",
      "resource_type": "job",
      "resource_id": "job_01HZ...",
      "metadata": {},
      "ip_address": "203.0.113.1",
      "created_at": "2026-03-28T12:00:00Z"
    }
  ],
  "pagination": { "next_cursor": "...", "has_more": true, "limit": 50 }
}
```

Audit log is append-only and read-only via API — no delete or update endpoints.

---

## Tenant

### Get Tenant
```
GET /v1/tenant
```
Returns the current tenant's configuration.

### Update Tenant
```
PATCH /v1/tenant
```
**Request:**
```json
{
  "name": "Acme Corp",
  "config": {
    "default_poll_interval_seconds": 30,
    "default_cert_lifetime_days": 90,
    "sso": {
      "enabled": true,
      "provider": "okta",
      "client_id": "...",
      "issuer_url": "https://acme.okta.com"
    }
  }
}
```

---

## Health & Meta

### Health Check
```
GET /v1/health
```
Unauthenticated. Returns server health for load balancer / uptime monitoring.

**Response `200`:**
```json
{
  "status": "ok",
  "version": "1.4.2",
  "timestamp": "2026-03-28T12:00:00Z"
}
```

---

### API Version Info
```
GET /v1/version
```
**Response `200`:**
```json
{
  "api_version": "v1",
  "server_version": "1.4.2",
  "min_agent_version": "1.0.0"
}
```

---

## Permission Reference

| Permission | Scope |
|---|---|
| `devices:read` | View devices and inventory |
| `devices:write` | Update device metadata |
| `devices:revoke` | Revoke agent certificates |
| `jobs:read` | View jobs and results |
| `jobs:create` | Create and cancel jobs |
| `jobs:retry` | Retry failed jobs |
| `packages:deploy` | Create package install/remove/update jobs |
| `inventory:read` | Read device inventory |
| `inventory:request` | Trigger on-demand inventory collection |
| `groups:read` | View groups and membership |
| `groups:write` | Create, update, delete groups; manage membership |
| `tags:read` | View tags |
| `tags:write` | Create, delete tags; manage device tags |
| `sites:read` | View sites |
| `sites:write` | Create, update, delete sites; manage membership |
| `users:read` | View users |
| `users:write` | Invite, update, deactivate users |
| `roles:read` | View roles |
| `roles:write` | Create, update, delete custom roles |
| `api_keys:read` | View API key metadata |
| `api_keys:write` | Create and revoke API keys |
| `enrollment_tokens:write` | Create and revoke enrollment tokens |
| `alerts:read` | View alert rules |
| `alerts:write` | Create, update, delete, toggle alert rules |
| `audit_log:read` | Read audit log |
| `tenant:read` | View tenant configuration |
| `tenant:write` | Update tenant configuration and SSO settings |
| `scheduled_jobs:read` | View scheduled jobs |
| `scheduled_jobs:write` | Create, update, delete, toggle scheduled jobs |
