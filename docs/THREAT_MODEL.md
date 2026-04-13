# Threat Model

Companion to `SECURITY.md` and `SEC_VALIDATION_FINDINGS.md`. This document enumerates threats against Moebius using STRIDE, maps each to the control that mitigates it, and cites the code reference or test that demonstrates the mitigation is live. Residual and accepted risks are called out explicitly.

Written after the Phase-2 security validation pass, so every entry reflects tested reality rather than aspiration. Findings are current as of 2026-04-13.

## 1. Purpose & Scope

The threat model covers the shipping code in this repository: the API server, the scheduler, the Go agent (poller, executor, local UI/CLI), the PostgreSQL schema, and the React web UI. Cross-cutting concerns (tenant isolation, audit integrity, supply chain) are handled in their own section.

**In scope**
- The runtime trust boundaries between agent, server, UI, and database.
- Authentication and authorization flows.
- Integrity of jobs, inventory, audit logs, and file transfers.
- DoS and resource exhaustion at the API layer.
- The agent update path *as implemented in-tree*.

**Out of scope** (from `SEC_VALIDATION.md § 5`)
- External pentest / red team.
- Performance DoS and load testing.
- Physical device security.
- End-user device compromise (attacker with local root on the endpoint).
- Third-party dependency audit beyond `govulncheck` / `npm audit`.
- **Release-artifact signing operations.** Moebius is a FOSS project and the upstream repository is not the primary release channel. Downstream packagers and distributors (package maintainers, Helm chart publishers, container-image builders) are responsible for signing the artifacts they ship. The in-tree tooling (`tools/keygen`, `tools/sign`, agent-side verification in `agent/executor/update.go`) is provided for packagers to use; the upstream CI placeholder key is explicitly not a production signing identity. Threats arising from mis-operation of a downstream packager's signing pipeline are outside this model.

## 2. System Overview

See `docs/HIGH-LEVEL_DESIGN.md` for the full diagram. The security-relevant topology is:

```
  end-user browser                operator / CLI                third-party integration
         │                              │                                │
         │ HTTPS (TLS 1.2+)             │ HTTPS + API key                │ HTTPS + API key
         ▼                              ▼                                ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │                            API Server (Go)                             │
  │  TLS term or passthrough │ mTLS (agent) │ API key + OIDC (users)       │
  │  RBAC + scope enforcement │ rate limit │ quota │ audit log writer      │
  └────────────────────────────────────────────────────────────────────────┘
           │                         │
           │ pgx (TLS required)      │
           ▼                         │
  ┌──────────────────┐               │
  │   PostgreSQL     │◄──────────────┘
  │ append-only audit│
  └──────────────────┘
           ▲
           │ pgx (TLS required)
  ┌──────────────────┐
  │    Scheduler     │  (leader-elected via PG advisory lock)
  │ no auth imports  │
  └──────────────────┘

  ┌──────────────────┐
  │      Agent       │  — outbound HTTPS poll only, no inbound port
  │  poller/executor │  — runs as root/SYSTEM
  │  local UI (127.0.0.1 only, per-device CA, Name Constraints)
  │  local CLI (Unix socket / named pipe, SDDL-restricted)
  └──────────────────┘
```

## 3. Trust Boundaries

Each boundary below is a place where data crosses a change of privilege, and therefore where authentication, integrity, and authorization must be enforced.

| # | Boundary | Crossing principal | Enforcement |
|---|---|---|---|
| TB-1 | Internet → API Server | Agent, operator, UI | TLS 1.2+, mTLS (agents) or API-key/OIDC (users), rate limiter before auth |
| TB-2 | API Server → PostgreSQL | Server process | pgx TLS (`verify-full` in production), DB user without `DROP RULE` grant |
| TB-3 | Scheduler → PostgreSQL | Scheduler process | Same DB TLS; scheduler has no auth imports and operates on pre-authorized rows only |
| TB-4 | API Server → Agent (job payload) | Server | Agent pulls via mTLS on check-in; no push channel |
| TB-5 | Agent → local OS (command execution) | Agent process | Agent runs as root/SYSTEM; CDM gate + job type whitelist before exec |
| TB-6 | End user → Agent Local UI | Browser on same host | Loopback bind, per-device CA with Name Constraints, session cookie |
| TB-7 | Operator → Agent Local CLI | Local shell | Unix socket `0o660` + `agent-users` group (Linux); SDDL SYSTEM+Admins (Windows) |
| TB-8 | Release pipeline → Agent (update) | Package maintainer | Ed25519 signature + SHA-256 verified before staging; downgrade rejected |
| TB-9 | Tenant A → Tenant B (logical) | API caller with key from tenant A | Every query scoped by `tenant_id` from auth context, never from request |

## 4. Assets

Valued by the defender; damage corresponds to CIA compromise.

| Asset | Stored where | Confidentiality | Integrity | Availability |
|---|---|---|---|---|
| Agent private keys | Agent disk (`0o600`) | High | High | Med |
| API keys (hashes) | `api_keys.key_hash` | High (raw key never stored) | High | Med |
| Enrollment tokens (hashes) | `enrollment_tokens.token_hash` | High | High | Low |
| Tenant data (devices/inventory/jobs) | PostgreSQL | Med–High | High | High |
| Audit log | `audit_log` | Med | **Very High** (append-only) | Med |
| CA private key (agent trust root) | Server disk (`0o600`) | **Very High** | **Very High** | High |
| Local-UI per-device CA key | Agent disk (`0o600`) | Med (blast radius capped by Name Constraints) | High | Low |
| OIDC session state | No server-side store; stateless JWT | N/A | High (signature) | N/A |
| Release artifacts + signatures | Downstream packager's pipeline | N/A here | High (agent verifies) | N/A here |

## 5. Threat Actors

| Actor | Capability | Goal |
|---|---|---|
| TA-1 Unauthenticated internet attacker | Can reach API server endpoints, no credentials | Probe, enumerate, exhaust resources, exploit unauth bugs |
| TA-2 Malicious tenant operator | Holds a valid API key within their own tenant | Cross to another tenant's data or escalate beyond their role |
| TA-3 Escalation-seeker within one tenant | Holds a scoped or low-permission API key | Gain admin or broaden scope |
| TA-4 Thief of agent certificate | Exfiltrated a single valid agent client cert | Impersonate the device until revocation |
| TA-5 Thief of API key | Exfiltrated a user API key | Act as that user until revocation/rotation |
| TA-6 Local user on agent host | Non-root user on a managed endpoint | Invoke the agent beyond their privilege, talk to local UI/CLI, suppress jobs |
| TA-7 Network-adjacent attacker at reverse proxy | Sits between load balancer and API server, or in the same L2 segment | Forge mTLS cert headers, strip TLS, MITM |
| TA-8 Insider with DB access | Direct PG credentials, not via the API | Read secrets, alter audit log, tamper with RBAC rows |
| TA-9 Compromised scheduler replica | Runs scheduler code with DB creds | Abuse the "trusts pre-authorized rows" contract |

Out of scope (see § 1): attacker with local root on an endpoint, downstream packager with a compromised signing pipeline.

## 6. STRIDE per Component

Each row names a threat, the component it lands on, the control that addresses it, and the evidence (code path or test) that proves the control is live. Remaining risk is noted in plain language.

### 6.1 API Server

| ID | Threat | STRIDE | Actor | Control | Evidence | Residual |
|---|---|---|---|---|---|---|
| API-S1 | Impersonate an agent by presenting someone else's client cert | S | TA-4 | Server verifies chain, expiry, revocation, *and* device revocation on every mTLS request; `VerifyClientCertIfGiven` enforces chain at TLS layer | `server/auth/mtls.go:49-115`; `TestCertLifecycle_RevokedCertRejected`, `TestCertLifecycle_RevokedDeviceRejected` | Window between theft and revocation. See I7 residual on passthrough mode. |
| API-S2 | Present forged mTLS cert header in passthrough mode | S | TA-7 | `ProxyCertSanitizer` strips `X-Client-Cert` from any source IP outside `TRUSTED_PROXY_CIDRS` before auth middleware runs | `server/auth/proxy.go` + 10 unit tests in `proxy_test.go` | Depends on correct `TRUSTED_PROXY_CIDRS` configuration — operator misconfiguration is the residual risk. |
| API-S3 | Replay a consumed enrollment token | S | TA-1 | `UPDATE enrollment_tokens SET used_at = ... WHERE used_at IS NULL RETURNING` is atomic; losers get `ErrNoRows` → 401 | `TestEnrollment_TokenReuse`, `TestSecurity_EnrollmentTokenRaceConcurrent` (10 goroutines, exactly 1 wins) | None. Append-only DB rule backs it. |
| API-S4 | Forge an OIDC token | S | TA-1 | JWKS signature verification, `aud`/`iss`/`exp` validation; tenant mapping comes exclusively from `users.sso_subject` (partial unique index), never from a token claim | `server/auth/oidc.go`; migration `005_users_sso_subject_unique.up.sql`; `TestSecurity_SSOSubjectProvisioning` | Depends on IdP JWKS trust anchor being correct. |
| API-T1 | SQL injection via API parameters | T | TA-1, TA-2 | Every query is a pgx parameterised statement. `ORDER BY` columns are whitelisted. No `fmt.Sprintf` with SQL keywords anywhere (grep-verified). | `SEC_VALIDATION_FINDINGS.md § 2.3`; SQL-injection negative tests in `security_test.go` | None at application layer. |
| API-T2 | Tamper with another tenant's data via parameter smuggling | T | TA-2 | `tenant_id` extracted from `auth.TenantIDFromContext`, never from request; `RequireTenant` middleware; all queries carry `AND tenant_id = $N` | `TestSecurity_CrossTenantDeviceAccessReturns404`, `TestSecurity_CrossTenantJobCreationBlocked` | None. Regressions caught by the multitenancy suite. |
| API-T3 | Tamper with the audit log via the app | T | TA-8 (via server) | Only `audit.LogAction` touches the table from code; DB rules (`004_audit_log_immutable.up.sql`) silently drop UPDATE/DELETE and a `BEFORE TRUNCATE` trigger rejects TRUNCATE | `TestSecurity_AuditLogNoUpdateOrDelete` | A superuser with `DROP RULE` grant can still tamper — production DB user must not hold that grant. |
| API-R1 | Operator denies having taken an action | R | TA-2 | Every write handler calls `audit.LogAction`; failures are counted via `audit_write_failures_total` and logged at error level | `server/audit/audit.go`; `TestLogAction_FailureCountedAndLogged` | Swallowed failures are surfaced but not blocking — a storm of DB errors could drop log rows. Operators must alert on the counter. |
| API-I1 | API key leaks in a response body or log | I | TA-1 | `models.APIKey.KeyHash` carries `json:"-"`; `store.ListAPIKeys` excludes the hash column; raw key is returned exactly once at creation | `TestSecurity_APIKeyHashNotInResponse` | None. |
| API-I2 | Stack trace or DB error exposed in 500 response | I | TA-1 | `server/api/respond.go` writes generic status text only; internal errors go to structured logs | Code review in `SEC_VALIDATION_FINDINGS.md § 2.7` | None. |
| API-I3 | MITM reads traffic between operator and API | I | TA-7 | `MinVersion: tls.VersionTLS12`; reverse proxy terminates with Let's Encrypt or cert-manager | `server/cmd/api/main.go` TLS config | Depends on correct TLS termination config — outside the code. |
| API-D1 | Unauth attacker floods endpoints | D | TA-1 | Per-IP rate limit (60 rpm) applied *before* auth; per-tenant (600 rpm) after auth; per-agent check-in limit (6 rpm); all run simultaneously | `server/ratelimit/`; `TestSecurity_RateLimit_*` | Not a protection against a large botnet — upstream CDN / WAF is still the right place for volumetric DoS. |
| API-D2 | Authenticated tenant fills the DB with devices / jobs / keys / large files | D | TA-2 | `server/quota/` enforces per-tenant ceilings on devices, queued jobs, API keys, and single-file size; defaults from `QUOTA_MAX_*`, overridable per tenant; 409 `quota_exceeded` on reject; jobs fan-out check is atomic | `server/quota/quota.go`; `tests/integration/quota_test.go` | Best-effort — COUNT(\*) is outside tx, so two concurrent creates can both pass by one. Acceptable for DoS mitigation; rate limiter provides the tight inner bound. |
| API-D3 | Request body exhausts memory | D | TA-1 | Global 8 MB `MaxBytes` middleware; per-route tighter caps on check-in (4 MB), log ingest (4 MB), job results (4 MB), chunk upload (6 MB + in-handler `MaxBytesReader`) | `TestSecurity_BodySize_*` | Individual `/v1` JSON CRUD endpoints not yet individually tightened below 8 MB — tracked in findings §2.3. |
| API-E1 | `roles:write` holder mints a role escalating beyond their grant | E | TA-3 | `roles.Create/Update` calls `auth.PermissionsSubset(callerPerms, req.Permissions)` for non-admin callers | `TestRBAC_RoleCreatePrivEscBlocked` and siblings | None. Landed during audit. |
| API-E2 | `users:write` holder assigns a higher role to another user or to self | E | TA-3 | `users.Update` blocks self-role-change and applies the same subset clamp on cross-user role assignment | Covered alongside E1 | None. |
| API-E3 | Non-admin deletes the bootstrap admin key | E | TA-3 | `apikeys.Delete` blocks non-admin callers from deleting `is_admin=true` keys via `store.GetAPIKey` | Covered in RBAC test suite | None. |
| API-E4 | Scope bypass — scoped key operates on out-of-scope device | E | TA-2, TA-3 | `auth.ResolveScope`, `DeviceInScope`, `FilterDeviceIDs`, `ScopeIsSubset`, `ValidateScopeTenant` consulted by every device-scoped handler | `TestSecurity_ScopedKeyCannotAccessOutOfScopeDevice`; AST test `TestRouter_V1BlockRequiresRBACOnEveryRoute` guarantees no `/v1` route is registered without an `rbac.Require(...)` decorator | None for the known-route shape. AST walker checks explicit verb methods; if routes are later registered via `r.Handle` or `r.Method`, the walker must be extended. |
| API-E5 | CSR smuggles a foreign subject or weak key | E | TA-1 | `SignCSR` validates the CSR signature, forces server-chosen `agentID` as CN/SAN (CSR-supplied subjects are ignored), and calls `validateAgentPublicKey` to enforce ECDSA P-256 | `TestSignCSR_RejectsNonP256Keys`, `TestSecurity_Enrollment_RejectsRSACSR`, `TestSecurity_Enrollment_RejectsP384CSR` | None. |

### 6.2 Scheduler

| ID | Threat | STRIDE | Actor | Control | Evidence | Residual |
|---|---|---|---|---|---|---|
| SCH-T1 | Scheduler is convinced to dispatch a job that was never authorized | T | TA-9 | Scheduler has no authz imports, operates on rows that were already authorized at creation time; the API server is the only writer of `scheduled_jobs` and `alert_rules` | `TestScheduler_NoAuthzImports` (fails compile if `server/rbac`, `server/auth`, or `server/api` is transitively imported) | Row-level authorization is enforced at API write time. If the API writes an under-authorized row, the scheduler will faithfully execute it — the API is the sole enforcer. |
| SCH-D1 | Two schedulers race and double-enqueue | D, T | TA-9 | Single active instance via PostgreSQL advisory lock; follower stands by | `server/cmd/scheduler/main.go` leader-election path | Standard PG advisory lock semantics; holder loss releases the lock automatically on session close. |
| SCH-D2 | Reaper pins resources by reaping too aggressively | D | — | Reaper timeouts are env-vars with documented defaults (`REAPER_DISPATCHED_TIMEOUT_SECONDS=300`, `REAPER_INFLIGHT_TIMEOUT_SECONDS=3600`) | `docs/Deployment_Instructions.md` | Operator tuning is the defense. |

### 6.3 Agent (poller + executor + update)

| ID | Threat | STRIDE | Actor | Control | Evidence | Residual |
|---|---|---|---|---|---|---|
| AG-S1 | Rogue server impersonates the real one | S | TA-7 | Agent TLS pins `RootCAs: serverCA`; no `InsecureSkipVerify` outside test files | Code review; grep-clean in `SEC_VALIDATION_FINDINGS.md § 2.5` | Depends on the server CA being correctly distributed during enrollment. Enrollment bootstrap uses a token delivered out of band. |
| AG-T1 | Malicious job payload escapes argv into shell | T, E | TA-2 (if the API allowed it) | Executor uses `exec.CommandContext` with argv, never `sh -c`; setuid package-helper validates names against a strict deny set | `agent/executor/exec.go`; `SEC_VALIDATION_FINDINGS.md § 2.3` | The attacker would need to get a malicious command past tenant-level RBAC; the agent doesn't defend against its own authorized tenant. |
| AG-T2 | Update binary is tampered with in flight | T | TA-1 + TA-7 | Agent verifies SHA-256 and Ed25519 signature of the staging file **before** replacing the current binary; old binary saved as `.previous` for rollback | `TestExecuteAgentUpdate_FullFlow`, `TestExecuteAgentUpdate_ChecksumMismatch`, `TestExecuteAgentRollback_Success` | Signing key trust anchor is the downstream packager's responsibility (see § 1). Upstream ships a placeholder `keys/release.pub` that **fails closed** — a signed update cannot verify against it. |
| AG-T3 | Downgrade attack via signed-but-old artifact | T | TA-7 | `update.go:39` rejects updates whose target version does not exceed the running version | `TestExecuteAgentUpdate_DowngradeRejected` | None. |
| AG-R1 | End user denies granting a CDM session | R | TA-6 | CDM actions write to `cdm_events` on the agent and to the audit log on the server via the check-in channel | `agent/cdm/` + `tests/integration/cdm_test.go` | The agent is the authoritative store; a user with local root on the endpoint can tamper with agent state. Out of scope (§ 1). |
| AG-I1 | Exfiltrate agent private key | I | TA-6 | Key stored at `0o600` under the agent's working directory; on Windows, ACL restricts to SYSTEM | `agent/auth/store.go` | Local root on the endpoint defeats this — out of scope. Revocation path is the recovery. |
| AG-D1 | Malicious server sends a huge job payload | D | TA-7 | Agent parses responses with bounded decoders; per-job timeout enforced | `agent/poller/poller.go` | Operator tuning via `JOB_TIMEOUT_DEFAULT`. |
| AG-E1 | Job runs while CDM is holding | E | TA-2 (via server) | Executor gate at `agent/executor/executor.go:68` fails closed on a nil CDM manager; CDM checked before each job starts | `TestRunJob_CDMGate_HoldsWhenNoSession`, `TestRunJob_CDMGate_AllowsWhenSessionActive`, `TestRunJob_CDMGate_NilManagerRefuses` | None. |
| AG-E2 | First-install binary is tampered with (no signature verification at install time) | T, E | TA-1 | Enrollment token + TLS to a trusted distribution channel; agent *does not* verify an installer signature | `SEC_VALIDATION_FINDINGS.md § I8 gap 3` | **Accepted.** The current upstream shipping model places this responsibility on the downstream packager. Users who need stronger guarantees should install from a signed package (deb/rpm/msi) whose signature is verified by the OS package manager. |

### 6.4 Agent Local UI & CLI

| ID | Threat | STRIDE | Actor | Control | Evidence | Residual |
|---|---|---|---|---|---|---|
| LUI-S1 | Non-loopback host reaches the local UI | S | TA-6 | Listener hard-coded to `127.0.0.1`; IPv6 loopback intentionally unsupported | `agent/localui/server.go:98`; `TestServerBindsLoopbackOnly` | None. |
| LUI-S2 | Rogue cert served for `localhost` | S | TA-6 | Per-device CA with critical `PermittedDNSDomains: ["localhost"]`, `PermittedIPRanges: [127.0.0.1/32]`, `MaxPathLen: 0` | `TestLocalCAGeneration`, `TestTLSConfig`, `TestCertRotation` | CA key on disk at `0o600` — local root can always mint additional localhost certs. Inherent to any per-device CA; Name Constraints cap blast radius. |
| LUI-T1 | Unprivileged user on the host uses the CLI | T, E | TA-6 | Unix socket `0o660` + `agent-users` group (Linux); named pipe SDDL restricted to SYSTEM + BUILTIN\Administrators (Windows) | `agent/localcli/` transport | Membership in `agent-users` is the admin-delegated trust boundary. |
| LUI-I1 | Session cookie exfiltrated by another local process | I | TA-6 | HTTPS cookie over loopback, HttpOnly, short session TTL | `agent/localui/session.go` | Local root on the endpoint defeats this — out of scope. |

### 6.5 PostgreSQL

| ID | Threat | STRIDE | Actor | Control | Evidence | Residual |
|---|---|---|---|---|---|---|
| DB-S1 | Service connects to an imposter Postgres | S | TA-7 | `docs/Deployment_Instructions.md § Database TLS` requires `verify-full` in production | `deploy/helm/values.yaml`, `deploy/docker-compose.yml` header comment | Operator tuning. Bundled docker-compose uses `disable` because Postgres is on a private docker network; doc explains why. |
| DB-T1 | Insider edits audit rows directly | T | TA-8 | `004_audit_log_immutable.up.sql` installs `DO INSTEAD NOTHING` rules for UPDATE/DELETE and a `BEFORE TRUNCATE` trigger | `TestSecurity_AuditLogNoUpdateOrDelete` | Superuser with `DROP RULE` can still tamper — production DB user must not hold that grant. This is a deployment-time configuration responsibility. |
| DB-I1 | Backup files leak credentials or PII | I | TA-8 | Out of tree; operator guidance in `docs/Deployment_Instructions.md` | — | Operator responsibility. |

### 6.6 Web UI

| ID | Threat | STRIDE | Actor | Control | Evidence | Residual |
|---|---|---|---|---|---|---|
| UI-T1 | Stored XSS via device name / job output | T | TA-2 | React escapes by default; no `dangerouslySetInnerHTML` usage (grep-clean) | Code review; `npm audit` clean | None at rendering layer. |
| UI-I1 | API key leaked via browser cache | I | TA-6 | Keys surfaced exactly once at creation; UI does not persist raw keys in local storage | Code review | User copy-paste discipline is the residual gap. |

## 7. Cross-Cutting Threats

### 7.1 Tenant Isolation

Every table carries `tenant_id`. Tenant is always extracted from `auth.TenantIDFromContext`. The multitenancy integration suite (`tests/integration/multitenancy_test.go`) walks the endpoint list and asserts 404-not-403 on cross-tenant access (hiding existence). Three cross-tenant reference gaps found during the audit (`installers.go:154,163`, `store/roles.go:96`) have been remediated. The known intentional exceptions are `agent_versions`, `agent_version_binaries`, and `installers` — platform-wide artifacts by design — and `ServeFileData`, an unauthenticated capability-URL endpoint whose security relies on UUID unguessability. Both are called out in `SEC_VALIDATION_FINDINGS.md § I4`.

### 7.2 Audit Integrity

Three layers: application (only `audit.LogAction` writes), DB rules (no UPDATE/DELETE/TRUNCATE through the service user), and observability (`audit_write_failures_total` Prometheus counter surfaces swallowed write errors). Retention and pruning must work around the append-only rules — `docs/AUDIT_RETENTION.md` is the canonical procedure (temporarily drop `audit_log_no_delete`, `DELETE`, recreate).

### 7.3 Supply Chain

Upstream hygiene:
- `go.mod` pins `toolchain go1.25.9`; `govulncheck` reports 0 called vulnerabilities.
- `npm audit` clean for the React UI (vite 8.0.5).
- Container images use distroless for `api`/`scheduler` and `nginx-alpine` for UI.
- GitHub Actions CI workflow uses `contents: read` permissions and does not reference signing secrets.

What is **not** upstream's responsibility (per § 1):
- Production release signing. `keys/release.pub` is a placeholder; the in-tree `release.yml` workflow is scaffolding for packagers to reference, not a production pipeline. Downstream packagers who choose to build from this repository must generate their own Ed25519 keypair, configure GitHub Environment isolation (or an equivalent on their CI platform), and register the public key with operators who deploy their builds. The agent-side verification path (`agent/executor/update.go`) will enforce whatever key is registered on the management server at deploy time.

Residual upstream risk: a downstream packager could ship a compromised binary. Operators defend against this by trusting only the packagers and registered signing keys they have vetted.

## 8. Residual & Accepted Risks

| # | Risk | Rationale | Compensating control |
|---|---|---|---|
| R-1 | Window between agent-cert theft and revocation | Inherent to any long-lived cert model | Short (90-day) cert lifetime; revocation via device status; audit log of every mTLS request |
| R-2 | Passthrough-mode mTLS relies on correct `TRUSTED_PROXY_CIDRS` | Operational simplicity outweighs a second sanitiser layer | `ProxyCertSanitizer` unit tests; `docs/SERVER_DEPLOYMENT_SPEC.md` warns about the setting |
| R-3 | Quota enforcement is best-effort (COUNT(\*) outside tx) | Two concurrent creates can both pass by one; acceptable for a DoS ceiling | Rate limiter provides tight inner bound; per-tenant override allows operators to set lower soft caps |
| R-4 | Audit write failures are counted, not blocking | Write-ahead audit would couple every API request to audit DB availability | `audit_write_failures_total` alert obligation documented in `docs/AUDIT_RETENTION.md` |
| R-5 | Superuser with `DROP RULE` can bypass audit-log immutability | Application-layer rule enforcement cannot constrain a DB superuser | Production DB user must not hold superuser / `DROP RULE` grants |
| R-6 | Local-UI CA key on agent disk | Any per-device CA model has this property | Name Constraints confine blast radius to `localhost`/`127.0.0.1`; `MaxPathLen: 0` |
| R-7 | First-install agent binary not signature-verified on the endpoint | Responsibility rests with the downstream packager / OS package manager | Operators should distribute the agent via signed OS packages (deb/rpm/msi) |
| R-8 | Production release signing operationalisation | Upstream is not the primary release channel | Downstream packagers own signing; agent-side verification path is ready |
| R-9 | `agent_signingkeys.go:29` looks up signing keys without `tenant_id` filter | Not currently exploitable — server picks the key ID in the update payload | Tracked in `SEC_VALIDATION_FINDINGS.md § I8`; cosmetic consistency fix in the queue |
| R-10 | OIDC is wired but provisioning is admin-only | Product decision: first SSO user must be provisioned via `PUT /v1/users/{user_id}/sso-subject` | Covered by `TestSecurity_SSOSubjectProvisioning` — tenant-admin endpoint is the intended onboarding path |

## 9. Change Log

- **2026-04-13** — Initial threat model, drafted after the Phase-2 security validation pass (`SEC_VALIDATION_FINDINGS.md`). Release signing threats framed as downstream-packager responsibility rather than upstream gap.
