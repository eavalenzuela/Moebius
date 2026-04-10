# Security Validation Findings

Companion to `SEC_VALIDATION.md`. Each entry summarises the outcome for one invariant or test category using the scheme from `SEC_VALIDATION.md § 3.3`:

- **Verified** — code review + tests confirm the property holds; no outstanding concerns.
- **Partial** — the property holds today, but there are gaps in coverage, tooling, or defense-in-depth worth tracking.
- **Gap** — a real shortfall in the implementation that is not yet remediated.
- **Accepted Risk** — a known partial or intentional miss, with rationale and compensating controls.

Evidence pointers link to the corresponding subsection of `SEC_VALIDATION.md` and to specific files, tests, and commits so this document can stand alone for auditors.

All findings below are current as of 2026-04-09.

---

## Section 1 — Stated Invariants

### I1 — Agents never receive inbound connections (poll-only)

**Status: Verified**

- **Code review:** `agent/localui/server.go:98` hard-codes `127.0.0.1`; IPC uses Unix socket / named pipe. Grep across `agent/` for `net.Listen`/`ListenAndServe` found only the two expected loopback listeners.
- **Tests:** `agent/localui/server_test.go:TestServerBindsLoopbackOnly` parses the runtime bind address and asserts it's a loopback IP literal.
- **Static enforcement:** `depguard` rule `agent-no-pprof` in `.golangci.yml` forbids `net/http/pprof` under `agent/**`, verified to fire on an injected violation.
- **Evidence:** `SEC_VALIDATION.md § I1`.

### I2 — CDM is enforced on the agent, server cannot bypass

**Status: Verified**

- **Code review:** `shared/protocol/checkin.go` carries no server→agent CDM fields. Executor gate at `agent/executor/executor.go:68` was tightened during this audit to fail-closed on a nil CDM manager. Callers of `Enable/Disable/GrantSession/RevokeSession` are confined to `agent/localcli` and `agent/localui`.
- **Unit tests:** `TestRunJob_CDMGate_HoldsWhenNoSession`, `TestRunJob_CDMGate_AllowsWhenSessionActive`, `TestRunJob_CDMGate_NilManagerRefuses` cover allow/deny and fail-closed.
- **Integration tests:** `tests/integration/cdm_test.go` — `TestCDM_JobsHeldWithoutSession`, `TestCDM_SessionGrantReleasesJobs`, `TestCDM_RevokeSessionHoldsNewJobs`, `TestCDM_SessionExpiryAllowsCompletionBlocksNewWork` exercise the full server behaviour across hold/grant/revoke/expiry.
- **Evidence:** `SEC_VALIDATION.md § I2`.

### I3 — All RBAC enforcement is in the API server; background processors trust pre-authorized rows

**Status: Verified** (scope-enforcement gap remediated during the audit)

- **Code review:** `server/rbac` is imported only by `server/api`, `server/cmd/api`, and their tests. The scheduler (cron, reapers, alert evaluator) has no authz imports and operates on DB rows directly.
- **Static test:** `server/cmd/scheduler/main_test.go:TestScheduler_NoAuthzImports` uses `go list -deps` to assert the scheduler binary does not transitively import `server/rbac`, `server/auth`, or `server/api`. Verified to fire on an injected violation.
- **Remediated during audit:** the pre-existing scope-enforcement gap (handlers never consulted `auth.ScopeFromContext`) was fixed via `server/auth/scope.go` (`ResolveScope`, `DeviceInScope`, `FilterDeviceIDs`, `ScopeIsSubset`, `ValidateScopeTenant`) and every device-scoped endpoint now consults it.
- **Residual follow-ups** (not gaps, tracked for future hardening): no AST-level test that every `/v1` write path is wrapped in `rbac.Require`; relies on reviewer diligence. Router is a single readable file today.
- **Evidence:** `SEC_VALIDATION.md § I3`, `SCOPE_ISSUE_REMEDIATION_PLAN.md`.

### I4 — Tenant isolation: `tenant_id` extracted from auth context, never from request params

**Status: Verified** (3 cross-tenant reference gaps fixed during audit)

- **Code review:** All 80+ handlers in `server/api/` use `auth.TenantIDFromContext`. mTLS resolves tenant via `devices.tenant_id` join; API keys resolve tenant via `api_keys.tenant_id`. `RequireTenant` middleware rejects unresolved tenants.
- **Fixes landed during audit:** `server/api/installers.go:154,163` (files / signing_keys lookups missing `AND tenant_id = $N`), `server/store/roles.go:96` (role-usage count ignored `tenant_id`).
- **Tests:** `tests/integration/multitenancy_test.go`, `TestSecurity_CrossTenantDeviceAccessReturns404`, `TestSecurity_CrossTenantJobCreationBlocked`.
- **Known intentional exceptions:** `agent_versions`, `agent_version_binaries`, `installers` are global by design (platform-wide artifacts). `ServeFileData` is an unauthenticated capability-URL endpoint (security relies on UUID unguessability). Both noted as acceptable in code review.
- **Evidence:** `SEC_VALIDATION.md § I4`.

### I5 — API keys and enrollment tokens stored only as SHA-256 hashes

**Status: Verified**

- **Schema:** `api_keys.key_hash` and `enrollment_tokens.token_hash` are the only credential columns; no plaintext storage anywhere.
- **Creation paths:** 192-bit API keys and 256-bit enrollment tokens both sourced from `crypto/rand`, hashed before insert, raw value returned exactly once.
- **Protections against leakage:** `models.APIKey.KeyHash` has `json:"-"`; `store.ListAPIKeys` excludes the hash column from `SELECT`; audit entries store only IDs, never raw material.
- **Static:** zero `math/rand` imports across `server/` and `shared/`.
- **Tests:** `TestSecurity_APIKeyHashNotInResponse`.
- **Evidence:** `SEC_VALIDATION.md § I5`.

### I6 — Enrollment tokens are single-use (atomically consumed)

**Status: Verified**

- **Atomicity:** `ValidateAndConsume` uses `UPDATE ... WHERE used_at IS NULL AND expires_at > $1 RETURNING ...` — the PostgreSQL row-level lock guarantees only one caller wins; losers get `pgx.ErrNoRows` → 401.
- **Single call site:** `server/api/enroll.go:61` is the only consumer.
- **Reaper policy:** scheduler deletes only `used_at IS NULL AND expires_at < now()` rows; used tokens are retained for audit.
- **Tests:** `tests/integration/enrollment_test.go:TestEnrollment_TokenReuse` (sequential), `TestSecurity_EnrollmentTokenRaceConcurrent` (10 goroutines, exactly 1 succeeds).
- **Evidence:** `SEC_VALIDATION.md § I6`.

### I7 — Agent certs: chain + expiry + revocation checked on every mTLS request

**Status: Verified** (with 1 accepted residual risk)

- **Code review:** `server/auth/mtls.go:49-115` performs cert-presence, CN, `NotBefore`/`NotAfter`, cert-revocation, and device-revocation checks on every request. `NewAgentTLSConfig` was changed during the audit from `RequireAnyClientCert` → `VerifyClientCertIfGiven` so Go's TLS layer verifies the chain against `ClientCAs` — the original setting required a cert but did not verify it.
- **CSR identity controls:** `SignCSR` validates the CSR signature, forces the server-chosen `agentID` as CN/SAN (CSR-supplied subjects are ignored), and (post-audit) calls `validateAgentPublicKey` to enforce ECDSA P-256.
- **Tests:** `TestCertLifecycle_Renewal`, `TestCertLifecycle_RevokedCertRejected`, `TestCertLifecycle_RevokedDeviceRejected`, `TestCertLifecycle_ExpiredCertRejected`, `TestEnrollment_FullFlow`, `TestSignCSR_RejectsNonP256Keys`, `TestSecurity_Enrollment_RejectsRSACSR`, `TestSecurity_Enrollment_RejectsP384CSR`.
- **Accepted risk (resolved):** Production passthrough mode. Chain verification is now performed by either (a) Go's TLS layer in `TLS_MODE=direct`, or (b) `MTLSMiddleware`'s `X-Client-Cert` header fallback with `ProxyCertSanitizer` stripping the header from untrusted CIDRs. Residual risk: passthrough mode relies on correct `TRUSTED_PROXY_CIDRS` configuration.
- **Evidence:** `SEC_VALIDATION.md § I7`.

### I8 — Release artifacts and agent updates verified via Ed25519 + SHA-256

**Status: Partial** (core agent-side verification correct; server-side enforcement and initial-install verification are gaps)

- **What is verified:**
  - Signing tooling (`tools/keygen`, `tools/sign`) uses `crypto/ed25519` + `crypto/rand`.
  - Agent update executor (`agent/executor/update.go:22-185`) verifies SHA-256 and Ed25519 signature on the staging file **before** replacing the current binary.
  - Signing-key registration endpoint (`server/api/signingkeys.go`) rejects non-Ed25519 PEM keys at upload time.
  - Tests: `TestExecuteAgentUpdate_FullFlow`, `TestExecuteAgentUpdate_ChecksumMismatch`, rollback coverage via `TestExecuteAgentRollback_Success` / `TestCheckPostRestart_*`.
- **Known gaps (tracked as accepted risk pending product decision):**
  1. **`keys/release.pub` is a placeholder.** Fails closed — signed updates will error until a real key is generated, registered, and used — but the signing pipeline is not yet operational.
  2. **Server never verifies uploaded file signatures.** `files.signature_verified` is inserted `FALSE` and never updated. `Signature` / `SignatureKeyID` are stored but unvalidated. Defense-in-depth shortfall, not a bypass (agent still verifies before staging).
  3. **Installer downloads have no agent-side signature verification.** First-install trust rests on the enrollment-token + TLS channel. A compromised CDN could serve a tampered installer.
  4. **`agent_signingkeys.go:29` looks up signing keys by ID without a `tenant_id` filter.** Inconsistent with tenant isolation elsewhere, though not exploitable because the server picks the key ID in the update payload.
- **Evidence:** `SEC_VALIDATION.md § I8`.

### I9 — Local UI bound to 127.0.0.1, per-device CA with Name Constraints

**Status: Verified**

- **Bind:** `agent/localui/server.go:98` hard-codes `127.0.0.1`; `ServerConfig.Port` is the only knob. IPv6 loopback is intentionally not supported.
- **Per-device CA:** ECDSA P-256 self-signed CA with critical `PermittedDNSDomains: ["localhost"]` and `PermittedIPRanges: [127.0.0.1/32]`, `MaxPathLen: 0`, stored at `0o600`.
- **Leaf cert:** `localhost` CN + SANs, ServerAuth EKU only, ~90d validity, auto-rotated within 30d of expiry.
- **IPC perms:** Linux Unix socket `0o660` + `agent-users` group; Windows named pipe SDDL restricted to SYSTEM + BUILTIN\Administrators.
- **Tests:** `TestLocalCAGeneration`, `TestLocalCAIdempotent`, `TestLocalhostCertIssuance`, `TestTLSConfig`, `TestCertRotation`, plus end-to-end `TestServerLoginLogout` / `TestServerBadLogin` / `TestServerCDMFlow` / `TestServerStaticFiles`.
- **Inherent risk:** CA key on disk at `0o600` — local root on the endpoint can always sign additional localhost certs. This is intrinsic to any per-device CA model; the Name Constraints cap the blast radius.
- **Evidence:** `SEC_VALIDATION.md § I9`.

### I10 — Audit log append-only

**Status: Verified** (DB-level hardening + handler audit coverage added during audit)

- **Code review:** Only `audit.LogAction` writes (`INSERT`) and `auditlog.List` reads (`SELECT`) reference the `audit_log` table anywhere in the codebase.
- **DB-level enforcement:** Migration `004_audit_log_immutable.up.sql` adds two PostgreSQL rules (`audit_log_no_update`, `audit_log_no_delete` → `DO INSTEAD NOTHING`) plus a `BEFORE TRUNCATE` trigger. A superuser could still `DROP RULE`, but the service user should not hold that grant in production.
- **Coverage remediation:** 8 handler files that previously performed writes without audit calls (`apikeys`, `users`, `roles`, `agent_jobs`, `groups`, `sites`, `tags`, `tenants`) now call `LogAction` on every write. Full action list in `SEC_VALIDATION.md § I10`.
- **Tests:** `TestSecurity_AuditLogNoUpdateOrDelete`.
- **Known operational concerns (not security gaps):** audit-log writes use `_ = h.audit.LogAction(...)` — errors are swallowed. For write-ahead audit compliance the policy would need to change. `audit_log` has no retention/partition policy; operators need guidance for long-lived deployments.
- **Evidence:** `SEC_VALIDATION.md § I10`.

---

## Section 2 — Test Categories

### 2.1 Authentication & Session Management

**Status: Verified** (with OIDC noted as dormant)

- API key paths and mTLS flow all have code review + integration tests (`TestSecurity_APIKeyHashNotInResponse`, `TestSecurity_ExpiredAPIKeyRejected`, `TestSecurity_RevokedAPIKeyRejectedImmediately`, `TestSecurity_ScopedKeyCannotAccessOutOfScopeDevice`, the `TestCertLifecycle_*` suite).
- Enrollment token race proven safe under concurrent use (`TestSecurity_EnrollmentTokenRaceConcurrent`).
- Enrollment scope copy is now transaction-wrapped with cross-tenant validation; covered by `TestSecurity_EnrollmentTokenScopeCopiedToDevice`, `TestSecurity_EnrollmentRejectsCrossTenantScope`, `TestSecurity_EnrollmentRollsBackOnBadScope`.
- **Dormant: OIDC.** `server/auth/oidc.go` is implemented and has been audit-reviewed (signature via JWKS, `aud`/`iss` validation, expiry enforced, tenant mapping comes from `users.sso_subject` only — never from a token claim). Migration `005_users_sso_subject_unique.up.sql` adds a partial unique index on `sso_subject` to close a subject-collision gap. The middleware is **not wired into the router**, `OIDCConfig` env vars are read but not consumed, and there is no SSO provisioning path yet, so no SSO user can authenticate today. Wiring it up is now a one-line router change rather than a re-audit. Tracked as a product decision, not a security gap.

### 2.2 Authorization (RBAC + Scope)

**Status: Verified** (privilege-escalation fixes landed during audit)

- Per-role matrix coverage via `rbac_test.go` (Super Admin, Tenant Admin, Operator, Technician, Viewer, custom roles).
- **Escalation paths fixed during audit:**
  - `roles.Create/Update` now calls `auth.PermissionsSubset(callerPerms, req.Permissions)` for non-admin callers — prevents `roles:write` holders from minting a role that escalates beyond their own grant.
  - `users.Update` blocks self-role-change and applies the same subset clamp on cross-user role assignment.
  - `apikeys.Delete` blocks non-admin callers from deleting `is_admin=true` keys via the new `store.GetAPIKey`.
- Tenant isolation regression suite asserts 404-not-403 on cross-tenant access to hide existence.

### 2.3 Input Validation & Injection

**Status: Verified**

- pgx parameterised queries everywhere (`fmt.Sprintf` + SQL keyword grep: zero hits).
- `ORDER BY` columns whitelisted; no user interpolation.
- Agent executor uses `exec.CommandContext` with argv, no `sh -c` wrapper. Setuid package-helper validates names against a strict deny set.
- Path traversal: file transfers use `filepath.Base` on agent-side; server-side storage keys are server-generated UUIDs passed through `filepath.Join` which normalises `..`.
- Body-size caps: global 8MB via `MaxBytes` middleware at chi root; per-route tightenings for check-in (4MB), log ingest (4MB), job results (4MB), chunk upload (6MB + in-handler `MaxBytesReader`). Coverage via `TestSecurity_BodySize_*`.
- **Residual future hardening:** tightening individual `/v1` JSON CRUD endpoints below the 8MB global cap would require restructuring the router (chi forbids `r.Use` after routes are registered). Global cap already satisfies the DoS requirement.

### 2.4 Cryptography & Key Management

**Status: Verified** (with I8 caveats)

- CA + cert signing: file perms `0o600`, cert usage / EKU correct, CSR key now locked to ECDSA P-256 via `validateAgentPublicKey` (see I7 fixes).
- Artifact signing: base64 Ed25519, agent verifies before staging. Gaps are the I8 items (placeholder key, no server-side file verification, no installer-download verification).
- Local UI CA: Name Constraints with `PermittedDNSDomainsCritical: true`, blast radius confined to `localhost` / `127.0.0.1` even on key compromise.
- Hashing: only `crypto/sha256` + `crypto/ed25519`; no MD5/SHA-1 for security purposes.
- Key rotation: `docs/KEY_ROTATION.md` consolidates procedures (Storage, Trigger planned, Trigger compromise, Procedure, Blast radius, Verification, Rollback) for Root CA, Intermediate CA, agent client certs, per-device local-UI CA, release signing key, API keys, and DB password.

### 2.5 Transport Security

**Status: Verified** (DB-TLS gap closed during this task)

- Server TLS config pins `MinVersion: tls.VersionTLS12`; passthrough-mode proxy-cert sanitizer (`server/auth/proxy.go`) covered by 10 unit tests in `proxy_test.go`.
- Agent client TLS verified (`RootCAs: serverCA`, no `InsecureSkipVerify` outside test files with `//nolint:gosec` annotations).
- **Closed during this remediation pass:** DB TLS. `docs/Deployment_Instructions.md § Database TLS` now documents `disable`/`require`/`verify-ca`/`verify-full` and requires `verify-full` (or at minimum `require`) in production. Helm `values.yaml` carries inline connection-string examples. `deploy/docker-compose.yml` makes `DATABASE_URL` env-overridable so operators can flip to `sslmode=require` without editing the compose file, with a header comment explaining why the bundled-Postgres default is `disable` (private docker network, evaluation only). Verification commands (`SHOW ssl`, `pg_stat_ssl`) included.

### 2.6 Agent Security Model

**Status: Verified** (CDM coverage completed during this remediation pass)

- Poll-only invariant (see I1).
- CDM integrity (see I2) — the server-respects-hold and session-expiry tests were added during this remediation pass; all CDM checkboxes now carry integration-test evidence.
- Agent update integrity: verify-before-stage order, `.previous` backup, downgrade rejection via `update.go:39`.

### 2.7 Secrets Hygiene

**Status: Verified**

- Grep audit for hardcoded credentials / test tokens in source: clean.
- `server/config` does not log config values; `main.go` logs only non-secret fields.
- `.gitignore` excludes `*.env`, `*.pem`, `*.key`, `keys/`.
- HTTP error responses return generic status text, not DB errors or stack traces.

### 2.8 Denial of Service & Resource Limits

**Status: Partial**

- **Verified:** two-tier rate limiting (per-IP 60 rpm + per-tenant 600 rpm, both active simultaneously), per-agent check-in limiter (6 rpm) on the checkin route, per-IP limit applied before authentication, per-job timeout enforcement on the agent, no regex compiled from user input. Tests in `server/ratelimit/`.
- **Gap:** per-tenant count-based resource limits (max devices, max jobs in queue, max file size, max API keys) are **not implemented**. These are quota counts rather than request-rate limits and require a separate feature implementation. Deferred beyond this validation pass.

### 2.9 Audit Log Integrity

**Status: Verified** (see I10).

### 2.10 Dependency & Supply Chain

**Status: Partial**

- **Verified:**
  - `go.mod` now declares `toolchain go1.25.9`. `govulncheck ./...` reports **0 called vulnerabilities** after the toolchain bump — the earlier scan against go1.25.0 had reported 13 called stdlib CVEs. Two non-called pgx findings (GO-2026-4771, GO-2026-4772 / CVE-2026-33815, CVE-2026-33816) remain in `-show verbose`; neither is reachable from any call path in our binaries and both carry `Fixed in: N/A` pending an upstream pgx release.
  - `npm audit` clean (vite 8.0.5).
  - Container images: distroless for api/scheduler, nginx-alpine for UI.
- **Gap:** CI pipeline release signing key isolation. The release signing key is not yet protected behind a GitHub Environment restricted to the release workflow. This interacts with the I8 placeholder-release-key gap.

---

## Outstanding Remediation Work

Items below are known shortfalls that fall outside the scope of this validation pass and are tracked here for the follow-on hardening queue.

1. **Release signing pipeline operationalisation (I8).** Generate a real Ed25519 keypair, check in `keys/release.pub`, register on the server, sign release artifacts, and enable server-side file signature verification (`files.signature_verified`). Add agent-side installer-download verification.
2. **Per-tenant count quotas (§ 2.8).** Implement max devices / jobs / files / API keys per tenant. Separate feature.
3. **CI release signing key isolation (§ 2.10).** GitHub Environment + workflow-level scoping for the signing credentials.
4. **Old-cert auto-revocation on renewal (I7 follow-up).** Optional. Would shorten the post-compromise window for agent keys from 90 days to near-zero at the cost of graceful-rollover flexibility.
5. **Audit write failures (I10 follow-up).** Swallowed errors in `_ = h.audit.LogAction(...)` mean audit writes can silently drop on DB outage. Compliance-sensitive deployments may need a write-ahead audit pattern.
6. **Audit retention / partitioning (I10 follow-up).** Operational guidance for long-running deployments.
7. **OIDC wiring + provisioning path (§ 2.1).** Middleware is audit-complete but dormant; needs a router wire-up and an SSO-subject provisioning endpoint before production SSO can be enabled.
8. **AST-level router assertion (I3 follow-up).** Walk `router.go` and assert every `r.Post/Put/Patch/Delete` under the authenticated route group has an `rbac.Require(...)` decorator. Low priority while router fits in one readable file.

---

## Deliverables Cross-Reference

Per `SEC_VALIDATION.md § 4`:

| # | Deliverable | Status |
|---|---|---|
| 1 | `SEC_VALIDATION_FINDINGS.md` | **This document.** |
| 2 | `tests/integration/security_test.go` + other new negative tests | Landed across `security_test.go`, `enrollment_test.go`, `cdm_test.go`, `cert_lifecycle_test.go`, `rbac_test.go`. |
| 3 | Code fixes with `sec:` commit prefix | Landed across the Phase-2 security commit series (most recent: `0d0a02f sec: Section 2 remediation`). |
| 4 | Updated `SECURITY.md` | Reviewed; no architectural changes required during this pass. |
| 5 | `docs/KEY_ROTATION.md` | Authored. Canonical key rotation reference. |
| 6 | `docs/THREAT_MODEL.md` (post-validation follow-on) | Deferred — blocked on closing the remaining I8 and § 2.8 gaps so the threat model reflects tested reality. |
