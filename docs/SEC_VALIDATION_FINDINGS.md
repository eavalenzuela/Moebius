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
- **AST-level enforcement:** `server/api/router_rbac_test.go:TestRouter_V1BlockRequiresRBACOnEveryRoute` parses `router.go` and asserts every HTTP verb call inside the `/v1` route block chains from `r.With(...)` containing an `rbac.Require(...)` argument. Three companion tests verify the walker itself flags missing `With`, `With` without `rbac.Require`, and accepts the positive case. End-to-end validated by temporarily stripping the decorator from `POST /users/invite` — the test pointed at the exact file:line.
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

**Status: Partial** (upstream server+agent verification landed; release-pipeline operationalisation deferred to downstream packagers — upstream is not the primary release channel)

- **What is verified:**
  - Signing tooling (`tools/keygen`, `tools/sign`) uses `crypto/ed25519` + `crypto/rand`.
  - Agent update executor (`agent/executor/update.go:22-185`) verifies SHA-256 and Ed25519 signature on the staging file **before** replacing the current binary.
  - Signing-key registration endpoint (`server/api/signingkeys.go`) rejects non-Ed25519 PEM keys at upload time.
  - **Server-side file signature verification.** `server/api/files.go:CompleteUpload` now verifies the Ed25519 signature against the registered signing key's public PEM (matched by `files.signature_key_id` + `tenant_id`) over the SHA-256 digest produced during chunk assembly, mirroring the agent verify path. Failure deletes the assembled object and returns `400 signature_invalid`; `files.signature_verified` is set to TRUE only on a real verify. Defense-in-depth: `Download` additionally refuses to serve any row that has a stored `signature` but `signature_verified=FALSE`. Parser lives in `server/api/signingkeys.go:parseEd25519PublicKeyPEM`.
  - **Tenant-scoped agent signing-key lookup.** `server/api/agent_signingkeys.go:Get` now scopes the `SELECT` by `tenant_id` resolved from mTLS context, aligning with every other `/v1/agents/*` handler.
  - Tests: `TestExecuteAgentUpdate_FullFlow`, `TestExecuteAgentUpdate_ChecksumMismatch`, rollback coverage via `TestExecuteAgentRollback_Success` / `TestCheckPostRestart_*`, plus new `TestSecurity_FileSignature_ValidSignatureAccepted`, `TestSecurity_FileSignature_InvalidSignatureRejected`, and `TestSecurity_AgentSigningKeyCrossTenantReturns404` in `tests/integration/security_test.go`.
- **Deferred to downstream packagers (upstream is not the primary release channel):**
  1. **`keys/release.pub` placeholder.** A real Ed25519 keypair, check-in of `release.pub`, and registration on the server are the responsibility of whoever operates the downstream release pipeline. The upstream agent verify path fails closed without an operational key, which is the correct behaviour for upstream.
  2. **Installer first-install download verification.** Agent-side verification of the *initial* installer payload depends on the downstream distribution channel (MSI/rpm/deb signing, S3 + signed manifest, etc.) — not something upstream can choose unilaterally. First-install trust currently rests on the enrollment-token + TLS channel.
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
- Artifact signing: base64 Ed25519. Agent verifies before staging; server now verifies uploaded file signatures at `CompleteUpload` time and refuses to serve any row with a stored signature that did not pass verification. Remaining I8 items (placeholder release key, installer-download verification) are deferred to downstream packagers.
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

**Status: Verified**

- **Rate limiting:** two-tier rate limiting (per-IP 60 rpm + per-tenant 600 rpm, both active simultaneously), per-agent check-in limiter (6 rpm) on the checkin route, per-IP limit applied before authentication, per-job timeout enforcement on the agent, no regex compiled from user input. Tests in `server/ratelimit/`.
- **Count/size quotas:** `server/quota/` enforces per-tenant ceilings on devices, queued jobs, API keys, and single-file size. Defaults come from `QUOTA_MAX_*` env vars (10k devices, 10k queued jobs, 100 API keys, 1 GB files); tenants can override via `TenantConfig.Quotas` JSONB; `-1` means unlimited. Rejections return 409 `quota_exceeded` from `api/enroll.go`, `api/apikeys.go` Create, `api/jobs.go` Create+Retry, and `api/files.go` InitiateUpload. Queued-job count deliberately excludes terminal states so historical backlog cannot permanently pin the cap. The jobs-create fan-out check is atomic — a rejected batch lands zero jobs. Unit tests in `server/quota/quota_test.go` cover the override overlay, error format, and nil-resolver no-op path. Integration tests in `tests/integration/quota_test.go` cover each endpoint's reject + allow paths.

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

1. **Release signing pipeline operationalisation (I8) — deferred to downstream packagers.** Upstream is not the primary release channel, so real-key generation, `keys/release.pub` check-in, and agent installer first-install verification belong to whoever operates the downstream distribution pipeline. ~~Server-side file signature verification~~ **landed** — `server/api/files.go:CompleteUpload` now verifies against the registered signing key and `Download` refuses unverified rows. ~~Tenant-scoping on `agent_signingkeys.Get`~~ **landed**. See I8 above.
2. ~~**Per-tenant count quotas (§ 2.8).**~~ **Landed.** `server/quota/` enforces max devices / queued jobs / API keys / single-file size per tenant, with global defaults via `QUOTA_MAX_*` env vars and per-tenant overrides in `TenantConfig.Quotas`. Enforcement wired into enroll, jobs create/retry, api-keys create, and file upload initiate. Unit + integration tests landed. See `SEC_VALIDATION.md` §2.8 for evidence.
3. **CI release signing key isolation (§ 2.10).** GitHub Environment + workflow-level scoping for the signing credentials.
4. ~~**Old-cert auto-revocation on renewal (I7 follow-up).**~~ **Landed.** `server/api/renew.go` now wraps the cert insert in a transaction and marks all prior un-revoked certs for the device as `superseded_by_renewal`. The agent receives the new cert in the same response so rollover remains graceful — revocation only bites if the stolen old cert is presented on a subsequent handshake. Verified by `tests/integration/cert_lifecycle_test.go:TestCertLifecycle_Renewal` (asserts 1 active / 1 superseded row and that the old cert is rejected).
5. ~~**Audit write failures (I10 follow-up).**~~ **Landed.** `server/audit/audit.go` now routes every write failure (and metadata-marshal failure) through a `reportFailure` helper that logs at error level and increments the new Prometheus counter `audit_write_failures_total`. Call sites no longer use the `_ = h.audit.LogAction(...)` pattern — a non-zero counter is the signal that audit rows were lost. Unit-tested by `server/audit/audit_test.go:TestLogAction_FailureCountedAndLogged` using a closed pool to force the error path.
6. ~~**Audit retention / partitioning (I10 follow-up).**~~ **Landed.** `docs/AUDIT_RETENTION.md` is the canonical operational guide: growth expectations, the periodic-pruning transaction pattern (temporarily drop `audit_log_no_delete`, `DELETE`, recreate), RANGE partitioning for high-volume deployments, pre-deletion archival, an alert example on `audit_write_failures_total`, and indexing guidance. Referenced from `SECURITY.md` and `CLAUDE.md`.
7. ~~**OIDC wiring + provisioning path (§ 2.1).**~~ **Landed.** `server/cmd/api/main.go` now builds the OIDC middleware via `auth.NewOIDCMiddleware(...)` and passes it into `RouterConfig.OIDC`; `server/api/router.go` installs it inside the `/v1` group before the API-key middleware. OIDC is a no-op passthrough when `OIDC_ISSUER_URL`/`OIDC_CLIENT_ID` are unset, so existing API-key deployments are unaffected. The SSO-subject provisioning endpoint is `PUT /v1/users/{user_id}/sso-subject` (guarded by `rbac.PermUsersWrite`), backed by `store.SetUserSSOSubject` which respects the partial unique index from migration 005. Integration-tested by `tests/integration/security_test.go:TestSecurity_SSOSubjectProvisioning` (link, conflict → 409, unlink, re-link after unlink) and `TestSecurity_SSOSubjectProvisioningRequiresAuth`.
8. ~~**AST-level router assertion (I3 follow-up).**~~ **Landed.** `server/api/router_rbac_test.go` parses `router.go` with `go/parser`, finds the `r.Route("/v1", ...)` block, and asserts every `r.Get/Post/Put/Patch/Delete` inside chains from `r.With(...)` containing an `rbac.Require(...)` argument. Three synthetic-source unit tests verify the walker correctly flags the three failure modes (no `With` at all, `With` without `rbac.Require`, and the positive case). End-to-end validated by temporarily stripping the decorator from `POST /users/invite` — the test pointed at `router.go:175 Post "/users/invite" — no r.With(...) chain`. Scope note: covers the explicit HTTP verb methods; if routes are later registered via `r.Handle` or `r.Method`, extend `httpVerbMethodNames` in the test.

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
| 6 | `docs/THREAT_MODEL.md` (post-validation follow-on) | **Landed.** STRIDE-per-component with evidence citations. Release-signing threats framed as downstream-packager responsibility (upstream is not the primary release channel). |
