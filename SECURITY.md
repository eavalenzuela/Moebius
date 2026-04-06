# Security

This document describes the security architecture and design decisions in Moebius.

## Transport Security

### Agent-to-Server (mTLS)

All agent communication with the server uses mutual TLS (mTLS). During enrollment, the agent generates an ECDSA P-256 keypair, submits a CSR, and receives a certificate signed by the server's intermediate CA. Every subsequent request presents this client certificate.

The server verifies the certificate chain, checks expiry, and queries the database for revocation status before processing any agent request. Certificate serial numbers and fingerprints are stored in the `agent_certificates` table.

Certificates are valid for 90 days. Agents renew before expiry by submitting a new CSR. Old certificates remain valid until their natural expiry (no immediate revocation on renewal), allowing graceful transition.

### API Clients (Bearer Token)

Operators and the web UI authenticate via API keys passed as `Authorization: Bearer sk_...` headers. API keys are never stored in plaintext — only the SHA-256 hash is persisted. Keys can be scoped to specific groups, tags, sites, or devices, and can have expiration dates.

### TLS Termination

In self-hosted deployments, Caddy terminates TLS (with automatic Let's Encrypt certificates) and proxies to the API server. In Kubernetes deployments, the ingress controller handles TLS termination with cert-manager.

The API server supports two modes: `passthrough` (behind a reverse proxy) and `direct` (terminates TLS itself).

## Authentication

### Agent Enrollment

Agents enroll using single-use enrollment tokens. Each token is:
- Generated as 32 random bytes, hex-encoded
- Stored as a SHA-256 hash (plaintext never persisted)
- Single-use (atomically consumed during enrollment)
- Time-limited (default 24 hours)
- Optionally scoped to specific groups, tags, or sites

The enrollment flow:
1. Operator creates a token via the API
2. Token is passed to the agent install script
3. Agent generates a keypair and CSR
4. Agent submits the token + CSR to `/v1/agents/enroll`
5. Server validates and consumes the token, signs the CSR, creates the device record
6. Agent receives its certificate and begins polling

### Certificate Revocation

Certificates can be revoked by marking the `revoked_at` timestamp in the database. Devices can also be revoked at the device level (status set to `revoked`). The mTLS middleware checks both conditions on every request.

A revoked agent must re-enroll with a new enrollment token to regain access.

## Authorization (RBAC)

All API authorization is enforced in the API server via middleware. The scheduler trusts that rows it operates on (scheduled jobs, alert rules, queued jobs) were pre-authorized at creation time by the API handler that accepted them — it does not re-check permissions. A regression test (`TestScheduler_NoAuthzImports` in `server/cmd/scheduler/main_test.go`) prevents the scheduler from transitively importing `server/rbac`, `server/auth`, or `server/api`.

### Predefined Roles

| Role | Scope |
|------|-------|
| **Super Admin** | All permissions |
| **Tenant Admin** | All permissions except cross-tenant operations |
| **Operator** | Device management, jobs, files, alerts, enrollment — no user/role management |
| **Technician** | Read devices, create jobs, read inventory — no write access to devices or management |
| **Viewer** | Read-only access to devices, jobs, inventory, groups, tags, sites |

Custom roles can be created with any subset of permissions.

### Admin Bypass

API keys with `is_admin=true` bypass all permission checks. This flag is set during bootstrap (`create-admin`) and should be limited to the initial setup key.

### Scope Restrictions

API keys can be scoped to specific groups, tags, sites, or devices via the `scope` field:

```json
{
  "scope": {
    "group_ids": ["grp_abc"],
    "tag_ids": ["tag_xyz"],
    "site_ids": ["site_123"],
    "device_ids": ["dev_456"]
  }
}
```

Scope is enforced per-request at the handler level (`server/auth/scope.go`). All scope fields are unioned to produce the set of allowed device IDs. A `nil` scope (unscoped key) has no restriction — equivalent to tenant-wide access. API keys with `is_admin=true` bypass scope checks.

**Enforcement points:**
- **Devices:** List is filtered to in-scope devices. Get/Update/Revoke return 404 for out-of-scope devices.
- **Jobs:** Create intersects resolved targets with scope (403 if no overlap). List is filtered. Get/Cancel/Retry check the job's device is in scope.
- **Inventory:** Device inventory is gated by `DeviceInScope`.
- **Groups/Tags/Sites:** List is filtered to scoped IDs. Get/Update/Delete check membership. Create is blocked for keys scoped to that resource type. Tag/device operations check `DeviceInScope`.
- **Scheduled jobs:** Create validates that the target overlaps with the key's scope.
- **Enrollment tokens:** Create validates that the token's scope is a subset of the key's scope.
- **Files:** Tenant-wide, not device-scoped. Access gated by job creation (which is scope-checked).
- **Alert rules:** Tenant-wide monitoring, not device-scoped. No scope enforcement needed.

## Multi-Tenancy

Every database table includes a `tenant_id` column. All queries are scoped to the authenticated tenant. There is no cross-tenant data access path through the API — tenant ID is extracted from the authenticated API key or agent certificate, never from request parameters.

Tenants have independent:
- Devices, groups, tags, sites
- Jobs and job results
- Users, roles, API keys
- Enrollment tokens
- Files and signing keys
- Alert rules and audit logs

## Customer Device Mode (CDM)

CDM provides end-user control over when management actions execute on their device. When CDM is enabled:

- The agent holds all incoming jobs in a local queue
- The server marks queued jobs as `CDM_HOLD`
- When the end user grants a session (with a time limit), jobs are released
- When the session expires or is revoked, new jobs are held again

CDM state is authoritative on the agent — the server reflects it but does not set it. This ensures that even if the server is compromised, the end user retains control over their device during active CDM.

## Artifact Signing

### Release Binaries

All release artifacts (agent binaries, tarballs) are signed with an Ed25519 key. The private key is stored in GitHub Actions secrets. The public key is committed to the repository (`keys/release.pub`) and registered on the management server.

The signing process:
1. SHA-256 hash of the artifact is computed
2. The hash is signed with the Ed25519 private key
3. The base64-encoded signature is published alongside the artifact

### File Transfer

Files uploaded through the chunked upload API have their SHA-256 checksum verified on completion. Files can optionally include an Ed25519 signature that is verified before the file is made available to agents.

### Agent Updates

When an agent receives an update job, it:
1. Downloads the new binary
2. Verifies the SHA-256 checksum
3. Fetches the signing key from the server and verifies the Ed25519 signature
4. Only then stages the binary for restart

If post-restart verification fails (e.g., version mismatch), the agent automatically rolls back to the previous binary and reports the failure on the next check-in.

## Network Security

### No Inbound Connections

Agents never accept inbound connections. All communication is initiated by the agent via outbound HTTPS requests to the server. This means:
- No open ports required on managed devices
- Agents work behind NAT and firewalls without configuration
- The attack surface on managed endpoints is minimal

### Agent Polling

Agents poll the server at a configurable interval (default 30 seconds). The server can adjust this interval via the check-in response. Polling is the only communication pattern — there is no push channel.

## Data Protection

### Secrets

- API keys: stored as SHA-256 hashes only
- Enrollment tokens: stored as SHA-256 hashes only
- CA private key: stored on disk with restricted permissions (0600), mounted read-only in containers
- Database password: provided via environment variable or Kubernetes secret
- Release signing key: stored in GitHub Actions secrets, never committed

### Audit Logging

All administrative actions are recorded in an append-only audit log table. Each entry includes the actor, action, resource, tenant, and timestamp. The audit log is queryable via the API (with `audit_log:read` permission).

Append-only integrity is enforced at two layers:
- **Application:** `server/audit/audit.go` only contains INSERT; no UPDATE/DELETE/TRUNCATE exists in the codebase.
- **Database:** Migration `004_audit_log_immutable.up.sql` adds PostgreSQL rules that silently discard UPDATE and DELETE, and an event trigger that rejects TRUNCATE.

### Database

PostgreSQL is the single source of truth. In production, use a managed PostgreSQL service with encryption at rest, automated backups, and restricted network access. The connection should use `sslmode=require` or stronger.
