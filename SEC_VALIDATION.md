# Security Testing & Secure Design Validation Plan

Scope: validate the security posture of Moebius (agent + server) against its stated invariants in `SECURITY.md` and `docs/HIGH-LEVEL_DESIGN.md`. This is a structured review pass — static analysis, targeted tests, and design review — not an external pentest. Results feed back into fixes or accepted-risk notes.

Authorization: this is authorized testing of our own codebase, development environment only. No production systems.

---

## 1. Stated Invariants to Validate

Pulled from `SECURITY.md` and `CLAUDE.md`. Each invariant must be closed out by ticking **at least one** of the three status boxes:

- **Code Reviewed** — a human read the relevant code paths and confirmed the invariant holds; note the file(s)/symbol(s) reviewed in the Evidence column.
- **Validation Tests Created** — an automated test exists that would fail if the invariant were broken; reference the test name.
- **Accepted Risk** — the invariant is partially or intentionally unmet; link to the rationale and any mitigating controls.

An invariant can carry more than one tick (e.g., both code-reviewed and test-backed is the ideal outcome).

| # | Invariant | Source | Code Reviewed | Tests Created | Accepted Risk | Evidence / Notes |
|---|---|---|:---:|:---:|:---:|---|
| I1 | Agents never receive inbound connections (poll-only) | HLD | ☑ | ☑ | ☐ | `agent/localui/server.go:98` hard-codes `127.0.0.1`; IPC is Unix socket / named pipe. Regression test: `TestServerBindsLoopbackOnly`. |
| I2 | CDM is enforced on the **agent**, server cannot bypass | HLD, SECURITY | ☑ | ☑ | ☐ | Protocol has no server→agent CDM fields. Executor gate at `executor.go:68` tightened to fail-closed. Unit tests: `TestRunJob_CDMGate_*`. |
| I3 | All RBAC enforcement is in the API server; background processors trust pre-authorized rows | HLD | ☑ | ☑ | ☐ | `server/rbac` imported only by `server/api` + `server/cmd/api`. Scheduler (cron + reapers + alerts) has no authz imports. Import regression test: `TestScheduler_NoAuthzImports`. **Scope enforcement gap — REMEDIATED.** `server/auth/scope.go` provides `ResolveScope`, `DeviceInScope`, `FilterDeviceIDs`, `ScopeIsSubset`. All device-scoped endpoints now enforce API key scope. See I3 notes + `SCOPE_ISSUE_REMEDIATION_PLAN.md`. |
| I4 | Tenant isolation: `tenant_id` extracted from auth context, never from request params | SECURITY | ☑ | ☑ | ☐ | All 80+ handlers use `auth.TenantIDFromContext`. mTLS resolves tenant from cert→device DB join. **3 cross-tenant reference gaps fixed.** See I4 notes. |
| I5 | API keys and enrollment tokens stored only as SHA-256 hashes (no plaintext) | SECURITY | ☑ | ☐ | ☐ | Schema: `key_hash` / `token_hash` columns, no plaintext column. `KeyHash` has `json:"-"`. `ListAPIKeys` excludes hash from SELECT. `crypto/rand` throughout, zero `math/rand`. See I5 notes. |
| I6 | Enrollment tokens are single-use (atomically consumed) | SECURITY | ☑ | ☑ | ☐ | Atomic `UPDATE...WHERE used_at IS NULL...RETURNING` in `ValidateAndConsume`. Single call site (`enroll.go:61`). Reaper preserves used tokens. Integration test: `TestEnrollment_TokenReuse`. See I6 notes. |
| I7 | Agent certs: chain + expiry + revocation checked on **every** mTLS request | SECURITY | ☑ | ☑ | ☑ | mTLS middleware checks expiry + DB revocation per request. Chain verified by Go TLS (`VerifyClientCertIfGiven`). **Fix:** `NewAgentTLSConfig` changed from `RequireAnyClientCert` → `VerifyClientCertIfGiven`. **Accepted risk:** production runs plain HTTP behind reverse proxy; mTLS only active in integration tests. See I7 notes. |
| I8 | Release artifacts and agent updates verified via Ed25519 signature + SHA-256 | SECURITY | �� | ☑ | ☑ | Agent update executor verifies SHA-256 + Ed25519 sig **before** staging binary. Signing toolchain uses `crypto/ed25519` + `crypto/rand`. **Gaps:** `keys/release.pub` is placeholder, server never verifies file signatures (`signature_verified` always FALSE), installer downloads have no agent-side sig verification. See I8 notes. |
| I9 | Local UI bound to 127.0.0.1 only, per-device CA with Name Constraints | HLD | ☑ | ☑ | ☐ | Hard-coded `127.0.0.1` bind (server.go:98). Per-device CA with `PermittedDNSDomains: ["localhost"]` + `PermittedIPRanges: [127.0.0.1/32]` (critical). IPC: Linux 0660 + agent-users group, Windows SDDL SYSTEM+Admins only. See I9 notes. |
| I10 | Audit log is append-only | SECURITY | ☑ | ☐ | ☐ | `audit.go` has INSERT only; `auditlog.go` has SELECT only. No UPDATE/DELETE/TRUNCATE on `audit_log` anywhere in codebase. **DB-level enforcement:** Migration 004 adds PostgreSQL rules rejecting UPDATE/DELETE + event trigger blocking TRUNCATE. **Remediated:** Audit logging added to all 8 previously unaudited handler files. See I10 notes. |

### Per-invariant detail

For each invariant, fill in the following as work progresses. Kept out of the main table to keep it scannable.

**I1 — Poll-only agents**
- Code Reviewed: `agent/localui/server.go` (Serve), `agent/ipc/listener_linux.go`, `agent/ipc/listener_windows.go`. Grep across `agent/` for `net.Listen|ListenAndServe|ListenUnix|ListenTCP|ListenPacket|Serve(|pprof|0.0.0.0` — only the two expected listeners exist.
- Tests Created: `TestServerBindsLoopbackOnly` in `agent/localui/server_test.go` — asserts the local UI bind address parses as an IP and is loopback. Fails if anyone introduces a `BindHost` config knob or swaps the literal for a hostname.
- Accepted Risk: n/a
- Notes / follow-up items surfaced during audit:
  - **Docstring drift risk**: `Serve()` godoc at `server.go:74` says "127.0.0.1:<port>" — the regression test is the real guard against drift, but worth noting the comment could fall out of sync with the literal.
  - **IPC socket permissions (adjacent to I9, not I1)**: Linux socket is `0660` with `agent-users` group; Windows pipe ACL is SYSTEM + BUILTIN\Administrators only. Not a network listener, so does not affect I1, but worth re-checking under I9 / local-auth review.
  - **Named-pipe path source**: `createListener(path)` takes `path` as a parameter — trace where `path` is constructed at the call site to confirm it's not user-controlled (defer to I9 audit).
  - **No pprof / debug listener present**: confirmed clean today. Added `depguard` rule `agent-no-pprof` in `.golangci.yml` that forbids `net/http/pprof` under `agent/**`, verified to fire on an injected violation. CI will now block future drift.
  - **No runtime assertion in production**: test enforces this at build/CI time only. Could add a startup-time `panic` if the parsed bind IP is non-loopback, as belt-and-braces. Low priority — the literal is right there.

**I2 — CDM agent-authoritative**
- Code Reviewed: `shared/protocol/checkin.go` (response has no CDM fields), `agent/cdm/cdm.go` (state machine), `agent/executor/executor.go:runJob` (gate), `agent/cmd/agent/main.go:260-390` (wiring), `agent/poller/poller.go:SetCDMState` (one-way reporter), callers of `Enable/Disable/GrantSession/RevokeSession` (only `agent/localcli/ipc.go` and `agent/localui/server.go`).
- Tests Created:
  - `TestRunJob_CDMGate_HoldsWhenNoSession` — job with CDM enabled + no session produces zero HTTP calls (no ack, no result).
  - `TestRunJob_CDMGate_AllowsWhenSessionActive` — positive case, 2 HTTP calls (ack + result).
  - `TestRunJob_CDMGate_NilManagerRefuses` — fail-closed: nil CDM manager → refuse.
- Accepted Risk: n/a
- Code changes made during audit:
  - **Tightened `runJob` to fail-closed** on nil CDM manager. Previously `if e.cdm != nil && !e.cdm.CanExecuteJob()` — a nil manager would let jobs through. Now split into two checks, nil manager refuses.
- Notes / follow-up items:
  - **All job types are gated equally** (exec, inventory_full, file_transfer, package_*, agent_update, agent_rollback). Per HLD "heartbeat, inventory, and telemetry always flow regardless of CDM" — that refers to check-in itself (heartbeat + delta inventory), not server-dispatched `inventory_full` jobs. Confirm this interpretation with product before closing.
  - **`agent_update` is CDM-gated**: an end user can indefinitely defer security-critical updates by holding CDM. Intentional per "user controls device" design, but worth flagging in threat model (user-vs-fleet tension).
  - **CDM state file permissions**: `persistLocked` writes with mode `0600` (good). Verify parent directory is also locked down as part of I9 audit.
  - **No server API can disable CDM**: confirmed by grep — `Enable/Disable/GrantSession/RevokeSession` callers are only `localcli` and `localui`. A future server-side "cdm_disable" job type would need to be explicitly added AND violate this invariant, which would be caught in review.

**I3 — RBAC in API server only**
- Code Reviewed: `server/rbac/rbac.go` (Require middleware), `server/api/router.go` (every `/v1` write path wrapped in `rbac.Require`), `server/scheduler/scheduler.go` (cron evaluator + alert evaluator + reapers — all operate on DB rows directly with no authz calls), grep for importers of `server/rbac` (only `server/api` + `server/cmd/api` + tests).
- Tests Created: `TestScheduler_NoAuthzImports` in `server/cmd/scheduler/main_test.go` — uses `go list -deps` to assert the scheduler binary does not transitively import `server/rbac`, `server/auth`, or `server/api`. Verified to fire on an injected violation. (Replaces the earlier `TestWorker_NoAuthzImports`, which was removed along with the worker binary when Phase 6 consolidated worker responsibilities into the scheduler.)
- Accepted Risk: n/a for the invariant as stated.
- Notes / follow-up items:
  - **Architectural change since original I3 audit:** the separate `worker` binary has been removed. Job dispatch is now inline in the API check-in handler (no message bus), and the scheduler absorbs all remaining background work: cron-scheduled job creation, alert-rule evaluation, requeuing stuck `dispatched` jobs, timing out stuck in-flight jobs, and deleting expired enrollment tokens. The invariant still holds — the scheduler operates only on rows whose creation was authorized at API time, and the regression test continues to prevent any future authz import drift.
  - **Scope enforcement gap (SEPARATE FINDING, not strictly I3)** — `server/auth/apikey.go:155` loads the API key's scope and places it in context via `ContextKeyScope`. `ScopeFromContext` is defined, exported, tested. **But no handler in `server/api/` calls it.** A scoped API key (e.g., scoped to group X) passes `rbac.Require(PermJobsCreate)` and can then create jobs targeting ANY device in the tenant. SECURITY.md advertises scoped keys as "fine-grained access control for automation and third-party integrations" — this guarantee is not actually enforced. Remediation belongs under a separate fix pass that touches every handler returning tenant-scoped resources or creating work against devices/groups/tags/sites. Top priority: `jobs.Create`, `jobs.Cancel`, `jobs.Retry`, `devices.List/Get/Update/Revoke`, group/tag/site listings, file operations. Also needs integration test coverage (zero scope tests exist today in `tests/integration/rbac_test.go`).
  - **Admin bypass exists** (`rbac.go:18`): `is_admin=true` API keys skip permission check entirely. Intentional per SECURITY.md, but this also means admin keys would skip any future scope check. Bootstrap admin key should be rotated and locked down — flag as a key-rotation-doc item.
  - **No static enforcement that every `/v1` write path has `rbac.Require`** — relies on reviewer catching missing middleware. Possible follow-up: an AST-based test that walks `router.go` and asserts every `r.Post/Put/Patch/Delete` under the authenticated route group has an `rbac.Require(...)` decorator. Lower priority — current router is one readable file.
  - **Agent endpoint authorization is identity-based, not RBAC** (`/v1/agents/*` use mTLS). These endpoints are scoped to "agent acts on its own behalf" — need to verify that `acknowledge` / `result` handlers check the job belongs to the authenticated agent (deferred to I7 audit — "per-request mTLS checks").

**I4 — Tenant ID from auth context**
- Code Reviewed: All 80+ handler functions in `server/api/` — every one uses `auth.TenantIDFromContext(r.Context())`. No handler reads `tenant_id` from request body, query params, or URL path. `server/auth/mtls.go:79-85` resolves tenant from `devices.tenant_id` via cert serial join (not from request). `server/auth/apikey.go:151` extracts tenant from `api_keys.tenant_id` (not from request). `server/auth/tenant.go` (`RequireTenant` middleware) rejects any request without a resolved tenant_id. `server/store/` — all tenant-scoped store methods take `tenantID string` parameter and include `WHERE tenant_id = $N`.
- Tests Created: Existing `tests/integration/multitenancy_test.go` covers device and job isolation (tenant A can't list/get tenant B's resources, returns 404 not 403).
- Accepted Risk: n/a — all identified gaps were fixed.
- Code changes made during audit:
  - **`server/api/installers.go:154`** — `SELECT EXISTS(SELECT 1 FROM files WHERE id = $1)` was missing `AND tenant_id = $2`. Fixed: a tenant admin creating an installer could previously reference another tenant's uploaded file. Added `AND tenant_id = $2`.
  - **`server/api/installers.go:163`** — Same pattern for `signing_keys WHERE id = $1`. Fixed: added `AND tenant_id = $2`.
  - **`server/store/roles.go:96-97`** — `DeleteRole` counted users/api_keys referencing a role_id across all tenants. Fixed: added `AND tenant_id = $2` to both subqueries.
- Notes / follow-up items:
  - **Global tables by design:** `agent_versions`, `agent_version_binaries`, and `installers` have no `tenant_id` column — they are intentionally platform-wide (same agent binary shared across tenants). In multi-tenant SaaS, this means any tenant admin can publish/yank agent versions or create installers visible to all tenants. Acceptable for self-hosted; in SaaS, these endpoints should be restricted to super-admin only. Flag for design review.
  - **`ServeFileData` is unauthenticated** (capability-URL pattern at `GET /v1/files/data/{file_id}`). File IDs are UUIDs generated server-side — security relies on unguessability. No tenant_id check because there is no auth context. This is the standard pre-signed-URL pattern. Acceptable risk for now; could add HMAC-signed URLs for defense-in-depth later.
  - **Belt-and-braces UPDATEs:** Several handlers perform a tenant-scoped SELECT to validate an ID, then UPDATE/DELETE by that ID without re-checking tenant_id (e.g., `jobs.go:379`, `checkin.go:139`, `files.go:222,237,309,358,361`). Within a single handler and request, this is safe (the ID was validated in the same request). Adding `AND tenant_id = $N` to these queries would be pure defense-in-depth. Low priority.
  - **Roles `tenant_id IS NULL` pattern:** System roles have `tenant_id IS NULL` and are visible to all tenants via `ListRoles` (line 16: `WHERE tenant_id IS NULL OR tenant_id = $1`). This is intentional — system roles (Super Admin, Tenant Admin, etc.) are shared. Custom roles are tenant-scoped.

**I5 — API keys / tokens hashed-only**
- Code Reviewed:
  - **Schema:** `api_keys.key_hash TEXT NOT NULL` (line 39), `enrollment_tokens.token_hash TEXT NOT NULL UNIQUE` (line 220). No plaintext column in either table.
  - **API key creation:** `server/api/apikeys.go:70-78` — `generateAPIKey()` uses `crypto/rand.Read(24 bytes)` → 192 bits entropy, SHA-256 hashed before INSERT. Raw key returned once in creation response (line 93). `models.APIKey.KeyHash` has `json:"-"` tag (line 27 of `shared/models/user.go`) — never serialized to JSON.
  - **Bootstrap admin key:** `server/cmd/api/main.go:255-262` — same pattern: `crypto/rand.Read(24 bytes)`, SHA-256 hash, only hash inserted. Raw printed to stdout once (line 277). Log at line 268 excludes raw key.
  - **Enrollment token creation:** `server/auth/enrollment.go:157-162` — `generateRawToken()` uses `crypto/rand.Read(32 bytes)` → 256 bits entropy. `hashToken()` at line 166 applies SHA-256. Only `token.TokenHash` inserted (line 71).
  - **Auth middleware lookup:** `server/auth/apikey.go:80-92` — hashes raw input via `hashAPIKey()`, then DB lookup by `WHERE k.key_hash = $1`. No constant-time compare needed — lookup is by hash pre-image, not comparison.
  - **Enrollment token validation:** `server/auth/enrollment.go:122` — `hashToken(rawToken)` then `WHERE token_hash = $2`. Same secure pattern.
  - **List endpoints:** `store.ListAPIKeys` (line 15) SELECT excludes `key_hash`. `enrollment_tokens.List` response struct (`tokenListItem`) has no hash or raw token field.
  - **Audit entries:** No raw key or token material in any `audit.LogAction` call — only IDs.
  - **No `math/rand`:** Grep across `server/` and `shared/` confirms zero imports of `math/rand`. All randomness is `crypto/rand`.
- Tests Created: n/a — `json:"-"` on `KeyHash` and `ListAPIKeys` excluding hash are structural guarantees. Existing `server/auth/apikey_test.go:TestScopeFromContext` exercises the auth middleware. Could add a round-trip test that creates a key and verifies the hash is not in the JSON response — low priority since `json:"-"` is deterministic.
- Accepted Risk: n/a
- Notes:
  - **Entropy levels:** API keys = 192 bits (`crypto/rand`), enrollment tokens = 256 bits (`crypto/rand`), prefixed IDs = 64 bits (`crypto/rand` — for collision avoidance, not security). All adequate.
  - **No key rotation mechanism:** API keys can be deleted and recreated, but there is no rotate-in-place operation. The bootstrap admin key must be manually replaced after first use. Flag for key rotation doc (deliverable §4.5).
  - **`hashAPIKey` is plain SHA-256, not bcrypt/scrypt:** Correct for this use case. API keys have 192 bits of entropy — brute-force against a SHA-256 hash is infeasible. Slow hashes (bcrypt) are for low-entropy secrets like passwords, not high-entropy API keys.

**I6 — Enrollment tokens single-use**
- Code Reviewed:
  - **Consumption:** `server/auth/enrollment.go:128-132` — single atomic SQL: `UPDATE enrollment_tokens SET used_at = $1 WHERE token_hash = $2 AND used_at IS NULL AND expires_at > $1 RETURNING ...`. PostgreSQL row-level lock ensures only one concurrent transaction succeeds; the loser gets `pgx.ErrNoRows` → 401.
  - **Single call site:** `ValidateAndConsume` is called only from `server/api/enroll.go:61`. No other code path consumes tokens.
  - **Peek (read-only):** `server/auth/enrollment.go:85-117` is `SELECT`-only, used by install script and installer download to validate without consuming. Peek→script→enroll flow is by design; if token is consumed between Peek and enrollment, the enrollment fails cleanly.
  - **Reaper preserves used tokens:** `server/scheduler/scheduler.go:204-205` — `DELETE FROM enrollment_tokens WHERE used_at IS NULL AND expires_at < $1`. Only deletes unused expired tokens. Used tokens (`used_at IS NOT NULL`) retained indefinitely for audit.
  - **Manual revocation:** `server/api/enrollment_tokens.go:145` — `DELETE ... WHERE id = $1 AND tenant_id = $2`. Operator action to revoke an unused token.
- Tests Created: `tests/integration/enrollment_test.go:TestEnrollment_TokenReuse` — first enrollment succeeds, second with same token gets 401. Sequential test (not concurrent goroutine race), but atomicity is guaranteed by the SQL pattern.
- Accepted Risk: n/a
- Notes:
  - **No concurrent race test:** Could add a parallel goroutine test that fires N enrollments simultaneously and asserts exactly 1 succeeds. Low priority — the atomicity is a PostgreSQL guarantee from the `UPDATE ... WHERE` pattern, not application-level locking.
  - **Expiry is checked at consumption time** (`expires_at > $1` in the WHERE clause), not just at creation. A token that expires between Peek and enroll will fail at enrollment.
  - **`token_hash` has a UNIQUE constraint** (schema line 220) — prevents insertion of duplicate hashes, providing a secondary defense against token collision (extremely unlikely with 256-bit `crypto/rand`).

**I7 — mTLS chain + expiry + revocation per request**
- Code Reviewed:
  - **mTLS middleware (`server/auth/mtls.go:49-115`):** Checks: (1) `r.TLS != nil && len(r.TLS.PeerCertificates) > 0` — cert present, (2) `cert.Subject.CommonName` non-empty — agent identity, (3) `time.Now()` vs `NotBefore`/`NotAfter` — expiry, (4) DB query joining `devices` + `agent_certificates` by device_id + serial_number — cert and device existence, (5) `revoked_at IS NOT NULL` — cert revocation, (6) `device.status == "revoked"` — device-level revocation. All six checks run on every request through the middleware.
  - **Chain verification (`server/auth/mtls.go:133-139`):** `NewAgentTLSConfig` sets `ClientAuth: tls.VerifyClientCertIfGiven` and passes the CA cert pool. When a client cert is presented, Go's TLS library verifies the chain against `ClientCAs` before populating `r.TLS.PeerCertificates`. **Fix applied:** was `tls.RequireAnyClientCert` which does NOT verify the chain (only requires a cert be presented). Changed to `VerifyClientCertIfGiven` which verifies chain when cert is given, and allows no cert for non-agent endpoints sharing the listener (enrollment, install scripts).
  - **Identity binding:** `checkin.go:36-41` extracts `agentID` and `tenantID` from auth context (set by mTLS middleware from cert CN and DB join), rejects if either is empty. `agent_jobs.go` handlers use `WHERE device_id = $2 AND tenant_id = $3` — agent can only acknowledge/submit results for its own jobs. `renew.go:38-44` same pattern. No handler reads agent identity from request body.
  - **Router separation (`server/api/router.go:47,62-83`):** Enrollment (`/v1/agents/enroll`) and install scripts are outside the mTLS route group — correct, since agents don't have certs yet at enrollment time. All post-enrollment agent endpoints (`/v1/agents/checkin`, `/v1/agents/renew`, `/v1/agents/jobs/*`, `/v1/agents/logs`, `/v1/agents/files/*`, `/v1/agents/signing-keys/*`) are inside `r.Use(mtls.Handler)`.
  - **Cert signing (`server/pki/ca.go:65-109`):** `SignCSR` — (1) CSR parsed and signature validated (`csr.CheckSignature()`, line 74), proving agent holds the private key. (2) **Server controls identity** — template uses `agentID` parameter for CN and DNS SAN; CSR's own Subject/SANs are ignored. Agent cannot choose its own identity via CSR manipulation. (3) `KeyUsage: x509.KeyUsageDigitalSignature` only — no key encipherment, no cert signing. (4) `ExtKeyUsage: x509.ExtKeyUsageClientAuth` only — cert valid for TLS client auth, nothing else. (5) `BasicConstraintsValid: true`, `IsCA` defaults to `false` — agent cert cannot sign other certs. (6) Serial: 128-bit random from `crypto/rand`. (7) Validity controlled by server (`defaultCertValidity = 90 days`), not by agent.
  - **Enrollment flow (`server/api/enroll.go:69-106`):** Device ID generated server-side (`models.NewDeviceID()`), passed to `SignCSR` as `agentID`. The agent's requested hostname is stored in the device record but does NOT affect the cert identity. Token consumed atomically before signing (I6 guarantee).
  - **Renewal flow (`server/api/renew.go:38-58`):** Renewal requires mTLS (inside middleware group). `agentID` comes from the existing cert's context, passed to `SignCSR` — ensures the renewed cert has the same identity. Old cert remains valid until natural expiry (no automatic revocation on renewal).
- Tests Created:
  - `TestCertLifecycle_Renewal` — enrolls agent, renews cert via mTLS, verifies new cert has same CN, ~90 day validity, and DB has 2 cert records.
  - `TestCertLifecycle_RevokedCertRejected` — revokes cert in DB (`revoked_at`), next check-in returns 401.
  - `TestCertLifecycle_RevokedDeviceRejected` — sets `device.status = 'revoked'`, next check-in returns 401.
  - `TestCertLifecycle_ExpiredCertRejected` — crafts a CA-signed cert with `NotAfter` in the past, TLS handshake or middleware rejects it.
  - `TestEnrollment_FullFlow` — full enrollment flow including cert issuance and first check-in.
- Accepted Risk:
  - **Production TLS architecture gap — RESOLVED.** Both deployment modes now supported:
    - `TLS_MODE=direct`: `server/cmd/api/main.go` calls `ListenAndServeTLS` with `NewAgentTLSConfig(caCertPool)`. Go's TLS layer verifies client cert chains directly.
    - `TLS_MODE=passthrough` (default): Server runs plain HTTP behind a reverse proxy. mTLS middleware falls back to reading `X-Client-Cert` header (PEM-encoded, optionally URL-encoded). Chain verification performed in `certFromHeader()` via `x509.Certificate.Verify` against the CA pool. `ProxyCertSanitizer` middleware strips `X-Client-Cert` from requests not originating from `TRUSTED_PROXY_CIDRS` (default: RFC 1918 + loopback) to prevent header spoofing.
    - **Remaining residual risk:** In passthrough mode, security depends on (a) the proxy correctly terminating mTLS and forwarding the cert, and (b) `TRUSTED_PROXY_CIDRS` being correctly configured. If the proxy is misconfigured to pass the header from untrusted sources, the sanitizer is the last line of defense.
- Code changes made during audit:
  - **`server/auth/mtls.go:135`** — Changed `tls.RequireAnyClientCert` → `tls.VerifyClientCertIfGiven`. `RequireAnyClientCert` requires a cert but does NOT verify its chain against the CA pool — any self-signed cert would pass the TLS handshake (revocation/expiry still caught by middleware, but chain trust was not enforced at the TLS layer). `VerifyClientCertIfGiven` verifies the full chain when a cert is present, and allows no cert for endpoints that don't need one.
  - **`server/auth/mtls.go` — Header fallback:** `MTLSMiddleware` now accepts a `caCertPool` parameter. When `r.TLS` is nil (proxy-terminated TLS), falls back to parsing PEM cert from `X-Client-Cert` header, verifying chain via `x509.Certificate.Verify`, then proceeding with same expiry/revocation checks.
  - **`server/auth/proxy.go` (new):** `ProxyCertSanitizer` middleware strips `X-Client-Cert` from requests not originating from trusted proxy CIDRs. Prevents untrusted clients from injecting forged cert headers.
  - **`server/cmd/api/main.go`** — `TLS_MODE=direct` now calls `ListenAndServeTLS` with `NewAgentTLSConfig(caCertPool)`.
  - **`server/config/config.go`** — Added `TRUSTED_PROXY_CIDRS` env var (default: RFC 1918 + loopback + IPv6 ULA).
  - **`server/api/router.go`** — Builds CA cert pool from loaded CA, passes to `NewMTLSMiddleware`. Adds `ProxyCertSanitizer` to global middleware chain when `TrustedProxyCIDRs` is configured.
- Notes / follow-up items:
  - **No CSR public key type validation:** `SignCSR` does not check the CSR's public key algorithm or strength. An agent could submit a weak RSA-1024 key, and the CA would sign it. In practice this is low risk: (a) enrollment is token-gated, (b) Go's TLS library enforces minimum key sizes at handshake time, (c) the CA itself uses ECDSA P-256. Could add a key-type/size check in `SignCSR` as defense-in-depth. Low priority.
  - **Old certs not revoked on renewal:** When an agent renews, the old certificate remains valid until it expires naturally. This is a design choice (allows graceful rollover), but means a compromised old key stays valid for up to 90 days after renewal. Could add optional auto-revocation of previous certs on renewal. Medium priority — depends on threat model for key compromise.
  - **Serial number format consistency:** Commit 5567335 fixed a serial number format mismatch between enrollment (which stores `hex.EncodeToString(serial.Bytes())`) and the mTLS middleware lookup (which also uses `hex.EncodeToString(cert.SerialNumber.Bytes())`). Both now use the same format. The `big.Int.Bytes()` representation omits leading zeros, which is consistent on both sides.
  - **No CRL or OCSP:** Revocation is checked per-request via direct DB lookup (not CRL distribution points or OCSP). This is appropriate for the architecture — the server is the only relying party and has direct DB access. No external verifiers need to check revocation.
  - **Cert-to-device binding is 1:N:** Multiple valid certs can exist for one device (renewal adds, doesn't replace). The mTLS middleware matches by both `device_id` AND `serial_number`, so a cert for device A cannot authenticate as device B even if both are in the same tenant.
  - **`VerifyClientCertIfGiven` vs `RequireAndVerifyClientCert`:** We use `VerifyClientCertIfGiven` rather than `RequireAndVerifyClientCert` because non-agent endpoints (enrollment, installer downloads) share the same TLS listener and should not require a client cert. The mTLS middleware (`mtls.Handler`) enforces the cert requirement for agent routes only.

**I8 — Artifact + update signature verification**
- Code Reviewed:
  - **Signing toolchain (`tools/keygen/main.go`, `tools/sign/main.go`):** `keygen` generates Ed25519 keypair via `crypto/ed25519.GenerateKey(crypto/rand.Reader)`. Private key written as raw 64 bytes at 0600. Public key base64-encoded for repo/server registration. `sign` reads private key, computes SHA-256 of the target file, signs the hash with `ed25519.Sign`, writes base64-encoded signature. Both tools correctly use `crypto/rand`, not `math/rand`.
  - **Agent update executor (`agent/executor/update.go:22-185`):** Full verification chain: (1) Download binary to staging path (line 84), (2) SHA-256 verification against declared hash — **before** staging (lines 93-115), (3) Ed25519 signature verification — **before** staging (lines 117-132), (4) `os.Chmod` to make executable (line 135), (5) Copy current → `.previous` backup (line 145), (6) Atomic rename staging → current (line 154). The order is critical: SHA-256 and signature are verified on the staging file before it replaces the current binary.
  - **`verifySignature` (`agent/executor/update.go:188-216`):** Fetches public key from server via mTLS-authenticated endpoint `/v1/agents/signing-keys/{key_id}`. Reads staged file, computes SHA-256 hash, decodes base64 signature, calls `ed25519.Verify(pubKey, hash[:], sig)`. Returns error on any failure. Key is PEM-decoded and validated as Ed25519 via `x509.ParsePKIXPublicKey` + type assertion (lines 244-258).
  - **Signing key registration (`server/api/signingkeys.go:40-93`):** `Create` endpoint validates PEM-encoded Ed25519 key via `validateEd25519PEM` — parses PEM, `x509.ParsePKIXPublicKey`, type-asserts `ed25519.PublicKey`, computes SHA-256 fingerprint. Non-Ed25519 keys rejected at upload time. Tenant-scoped storage.
  - **Agent-facing key endpoint (`server/api/agent_signingkeys.go:24-42`):** `GET /v1/agents/signing-keys/{key_id}` — returns public key PEM. Inside mTLS route group. **Note:** query uses `WHERE id = $1` without tenant_id filter — signing keys are looked up by ID only. Since agent updates are dispatched by the server (which supplies the key_id), and signing keys are tenant-scoped on creation, this is low risk but inconsistent with the tenant isolation pattern.
  - **Installer signature endpoint (`server/api/installers.go:272-286`):** `GET /v1/installers/{os}/{arch}/{version}/signature` returns the stored signature and key ID. The installer creation endpoint (`Create`) stores signature + key_id but does NOT verify the signature server-side.
  - **File model (`shared/models/file.go:16-30`):** `File` struct has `Signature`, `SignatureKeyID`, and `SignatureVerified` fields. Files are inserted with `signature_verified = FALSE` and **never updated to TRUE**. No server-side signature verification exists for uploaded files.
- Tests Created:
  - `TestExecuteAgentUpdate_FullFlow` — end-to-end: generates Ed25519 keypair, signs binary hash, mock server serves binary + key, executor downloads, verifies SHA-256 + signature, stages binary, writes pending update. Comprehensive positive test.
  - `TestExecuteAgentUpdate_ChecksumMismatch` — SHA-256 mismatch causes failure, staging file cleaned up.
  - `TestExecuteAgentUpdate_InvalidPayload` / `MissingVersion` / `NoPlatform` — negative pre-flight tests.
  - `TestExecuteAgentRollback_Success` / `NoPlatform` / `NoPrevious` — rollback path tests.
  - `TestCheckPostRestart_VersionMatch` / `VersionMismatch` / `DeadlineExceeded` — post-restart verification.
- Accepted Risk:
  - **`keys/release.pub` is a placeholder.** The file contains a comment "PLACEHOLDER — run 'go run ./tools/keygen' to generate". No real keypair has been generated. This means signature verification in production would fail until a real key is generated, registered on the server, and used to sign binaries. Not a security issue per se (it fails closed), but the signing pipeline is not yet operational.
  - **Server does NOT verify file signatures.** Files are inserted with `signature_verified = FALSE` and no code path sets it to `TRUE`. The `Signature` and `SignatureKeyID` fields on files are stored but never validated. This means a malicious API key holder could upload a file with a fake signature, and it would be stored without error. The agent-side update executor verifies signatures independently, so this is defense-in-depth gap, not a bypass — but a signed file could appear "signed" in the UI without actual verification.
  - **Installer downloads have no agent-side signature verification.** The initial install flow (enrollment script downloads installer) does not verify signatures — it trusts the server. The `agent/installer/` package has no signature or checksum logic. This is acceptable for initial install (the enrollment token + TLS channel provide trust), but means a compromised download server could serve a tampered installer binary.
  - **Agent-facing signing key endpoint lacks tenant_id filter.** `agent_signingkeys.go:29` queries `WHERE id = $1` without `AND tenant_id = $2`. An agent from tenant A could theoretically fetch a signing key belonging to tenant B by ID. Since key IDs are server-generated UUIDs and the server controls which key_id is placed in the update payload, this is not exploitable in practice, but it's inconsistent with the tenant isolation pattern.
- Notes / follow-up items:
  - **Signature is over the SHA-256 hash, not the raw file.** Both `tools/sign` and `agent/executor/update.go:verifySignature` sign/verify `SHA-256(file)`, not the raw file bytes. This is the standard pattern for Ed25519 on large files (Ed25519 operates on messages, not digests, but hashing first is standard for large inputs). The agent also independently verifies SHA-256, so both hash integrity and signature authenticity are checked.
  - **Public key fetched at verification time.** The agent fetches the signing key from the server during update execution, not at install time. This means the server could theoretically serve a different public key to match a tampered binary. Mitigation: the channel is mTLS-authenticated, so only the legitimate server can serve keys. A compromised server is a game-over scenario regardless. For defense-in-depth, could pin a known release key at install time (from `keys/release.pub`).
  - **No key rotation support for signing keys.** Keys can be registered and deleted, but there's no versioning or overlap mechanism. During rotation, the old key must stay registered until all agents have updated past binaries signed with it.
  - **Downgrade protection:** `update.go:39` rejects `p.Version <= version.Version` unless `Force` is set. This prevents version downgrade via normal update jobs. Rollback is only via the dedicated rollback path (which restores the `.previous` binary). Per the SEC_VALIDATION checklist item: "Version downgrade policy: downgrade is only permitted via the rollback path."

**I9 — Local UI loopback + Name Constraints**
- Code Reviewed:
  - **Bind address (`agent/localui/server.go:98`):** `fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)` — hard-coded loopback literal. `ServerConfig.Port` is the only configurable field; the host portion is not configurable and cannot be changed without modifying source. The value is passed directly to `net.Listen("tcp", addr)`.
  - **Per-device CA (`agent/localui/localca.go:81-130`):** `generateCA()` creates an ECDSA P-256 self-signed CA with critical Name Constraints:
    - `PermittedDNSDomainsCritical: true` — Name Constraints extension is marked critical (RFC 5280 §4.2.1.10: compliant verifiers MUST enforce it)
    - `PermittedDNSDomains: ["localhost"]` — CA can only sign certs with DNS SAN matching "localhost"
    - `PermittedIPRanges: [127.0.0.1/32]` — CA can only sign certs with IP SAN matching exactly 127.0.0.1
    - `IsCA: true`, `MaxPathLen: 0`, `MaxPathLenZero: true` — can sign leaf certs but cannot create sub-CAs
    - `KeyUsage: KeyUsageCertSign | KeyUsageCRLSign` — appropriate for a CA
    - Validity: ~10 years. Key stored at `0o600`.
  - **Leaf cert (`agent/localui/localca.go:132-182`):** `issueCert()` creates a leaf cert with:
    - CN: `localhost`, DNSNames: `["localhost"]`, IPAddresses: `[127.0.0.1]` — all within Name Constraints
    - `KeyUsage: DigitalSignature`, `ExtKeyUsage: [ServerAuth]` — cannot sign certs, only serve TLS
    - Validity: 90 days. Auto-rotates when within 30 days of expiry.
    - Signed by the per-device CA.
  - **TLS config (`localca.go:70-79`):** `TLSConfig()` loads the leaf cert+key, sets `MinVersion: tls.VersionTLS12`.
  - **IPC socket permissions:**
    - Linux (`agent/ipc/listener_linux.go:16-41`): Unix socket at `0o660` with `agent-users` group. No world access. Group set via `user.LookupGroup("agent-users")` + `syscall.Chown`.
    - Windows (`agent/ipc/listener_windows.go:14-30`): Named pipe with SDDL `D:(A;;GA;;;SY)(A;;GA;;;BA)` — only SYSTEM and local Administrators. No ordinary user access.
  - **Trust store installation (`agent/localui/truststore_linux.go`):** Best-effort installation of CA cert into system trust store (Debian or RHEL paths). Non-fatal on failure — browsers show warnings but the security model is unaffected. CA cert is world-readable (`0o644`) as needed for trust store; CA key is `0o600`.
  - **Cookie security (`server.go:243-250`):** Session cookie has `HttpOnly: true`, `Secure: true`, `SameSite: StrictMode`. Cannot be read by JavaScript, only sent over HTTPS, not sent on cross-origin requests.
- Tests Created:
  - `TestServerBindsLoopbackOnly` — regression test: parses actual listen address, asserts it's a loopback IP literal (not a hostname, not 0.0.0.0). Explicitly fails with invariant reference message.
  - `TestLocalCAGeneration` — verifies CA properties: `IsCA=true`, CN, `KeyUsageCertSign`, Name Constraints (`PermittedDNSDomains=["localhost"]`, `PermittedIPRanges=[127.0.0.1/32]`), ~10yr validity, key permissions `0o600`.
  - `TestLocalCAIdempotent` — second `EnsureCA()` does not regenerate (stable key).
  - `TestLocalhostCertIssuance` — verifies leaf: CN=localhost, SANs (DNS + IP), not a CA, ~90 day validity, chain verifies against CA with `DNSName: "localhost"`.
  - `TestTLSConfig` — verifies config has 1 cert and `MinVersion=TLS12`.
  - `TestCertRotation` — cert recreation after removal.
  - `TestServerLoginLogout` / `TestServerBadLogin` / `TestServerCDMFlow` / `TestServerStaticFiles` — functional tests exercising the HTTPS server end-to-end.
- Accepted Risk: n/a — invariant holds cleanly.
- Notes / follow-up items:
  - **IPv6 loopback (`::1`) not in Name Constraints.** The CA's `PermittedIPRanges` only includes `127.0.0.1/32`. The leaf cert only includes `127.0.0.1` as an IP SAN. The server binds to `127.0.0.1` (IPv4 only). This is consistent — IPv6 loopback is not supported. If IPv6 support were added later, both the Name Constraints and the bind address would need updating.
  - **Name Constraint enforcement depends on the verifier.** The `PermittedDNSDomainsCritical: true` flag means compliant TLS implementations MUST reject certs for non-localhost domains signed by this CA. All major browsers and Go's `crypto/x509` enforce this. However, non-compliant custom verifiers could ignore it. This is a standard X.509 reliance, not unique to Moebius.
  - **CA key on disk.** The per-device CA key is stored in the agent's data directory at `0o600`. A local root user or the agent process could read it and sign additional localhost certs. This is inherent to any per-device CA model — the CA exists to avoid requiring an external cert authority for localhost HTTPS. The Name Constraints limit blast radius even if the key is compromised.
  - **IPC socket path is a parameter (`agent/ipc/server.go`):** The socket path comes from `platform.SocketPath()`, which is constructed from the platform's runtime directory. Traced to `agent/platform/linux/linux.go` and `agent/platform/windows/windows.go` — both use fixed paths under the agent's install directory, not user-controlled input.

**I10 — Audit log append-only**
- Code Reviewed:
  - **`server/audit/audit.go`:** Single write path — `LogAction()` at line 31 executes `INSERT INTO audit_log (...)`. No UPDATE, DELETE, or TRUNCATE statements. The `Logger` struct exposes only `LogAction` — no methods to modify or remove entries.
  - **`server/api/auditlog.go`:** Read-only handler — `List()` at line 39 executes `SELECT ... FROM audit_log` with cursor pagination and filters (tenant_id, actor, action, resource_type, date range). No write operations. Endpoint is `GET /v1/audit-log`.
  - **Full codebase grep:** Searched all `.go` files for `UPDATE.*audit_log`, `DELETE.*audit_log`, `TRUNCATE.*audit_log` — **zero matches**. Only two references to the `audit_log` table in the entire codebase: the INSERT in `audit.go:31` and the SELECT in `auditlog.go:39`.
  - **Schema (`001_initial_schema.up.sql:178-189`):** Standard table with `id, tenant_id, actor_id, actor_type, action, resource_type, resource_id, metadata, ip_address, created_at`. No triggers, row-level security, or DB-level write restrictions beyond the table definition.
  - **Audit coverage analysis — handlers WITH audit logging (LogAction calls):** `checkin.go`, `enroll.go`, `renew.go`, `jobs.go` (create/cancel/retry), `scheduled_jobs.go` (CRUD), `enrollment_tokens.go` (create/revoke), `devices.go` (revoke), `files.go` (upload/complete), `signingkeys.go` (create/delete), `alert_rules.go` (CRUD), `agent_versions.go` (create/delete), `update_policies.go` (create/delete), `rollouts.go` (create), `device_rollback.go` (rollback), `installers.go` (create).
  - **Audit coverage analysis — handlers WITHOUT audit logging:**
    1. **`apikeys.go`** — API key create and delete. **Security-sensitive:** API key creation/revocation is a high-value audit event (access credential lifecycle).
    2. **`users.go`** — User invite, update, deactivate. **Security-sensitive:** user lifecycle and privilege changes.
    3. **`roles.go`** — Role create, update, delete. **Security-sensitive:** RBAC role definitions control access boundaries.
    4. **`groups.go`** — Group create, update, delete, add/remove members.
    5. **`sites.go`** — Site create, update, delete.
    6. **`tags.go`** — Tag create, delete.
    7. **`tenants.go`** — Tenant update.
    8. **`agent_jobs.go`** — Job acknowledge and result submission. Has `audit *audit.Logger` field injected but **never calls** `LogAction()`. Agent-side job state transitions are unaudited.
- Tests Created: None. The invariant is structural (grep-verifiable) rather than behavioral. A regression test could grep for UPDATE/DELETE on audit_log in Go source, but this is better enforced by code review.
- Accepted Risk:
  - **DB-level enforcement — RESOLVED.** Migration `004_audit_log_immutable.up.sql` adds: (1) `CREATE RULE audit_log_no_update` — silently rejects UPDATE, (2) `CREATE RULE audit_log_no_delete` — silently rejects DELETE, (3) `CREATE EVENT TRIGGER no_truncate_audit_log` — raises exception on TRUNCATE. Even if the application has a bug or is compromised via SQL injection, audit entries cannot be modified or removed through normal DML. **Note:** A superuser can still `DROP RULE` or `ALTER TABLE ... DISABLE RULE`, but that requires DDL privileges the service user should not have in production.
  - **8 handler files have write operations with no audit logging.** The three most security-sensitive gaps are: (1) `apikeys.go` — API key create/delete should be audited (credential lifecycle), (2) `users.go` — user invite/update/deactivate should be audited (identity lifecycle), (3) `roles.go` — role create/update/delete should be audited (RBAC changes). The remaining 5 (`groups`, `sites`, `tags`, `tenants`, `agent_jobs`) are lower priority but still represent operational visibility gaps.
- Notes / follow-up items:
  - **Append-only invariant holds at the application layer.** Code review confirms no code path modifies or deletes audit log entries. The invariant is clean in code.
  - **DB-level hardening — IMPLEMENTED.** Migration `004_audit_log_immutable.up.sql` (in both `server/migrate/sql/` and `deploy/migrations/`) adds PostgreSQL rules that silently discard UPDATE and DELETE operations on `audit_log`, plus an event trigger that raises an exception on TRUNCATE attempts. This provides defense-in-depth against application bugs or SQL injection targeting the audit log.
  - **Audit coverage gap — REMEDIATED.** All 8 previously unaudited handler files now have `LogAction` calls on every write operation:
    - `apikeys.go` — `api_key.create`, `api_key.delete`
    - `users.go` — `user.invite`, `user.update_role`, `user.deactivate`
    - `roles.go` — `role.create`, `role.update`, `role.delete`
    - `agent_jobs.go` — `job.acknowledge`, `job.result` (already had audit field, now calls LogAction)
    - `groups.go` — `group.create`, `group.update`, `group.delete`, `group.add_devices`, `group.remove_device`
    - `sites.go` — `site.create`, `site.update`, `site.delete`, `site.add_devices`, `site.remove_device`
    - `tags.go` — `tag.create`, `tag.delete`, `tag.add_to_device`, `tag.remove_from_device`
    - `tenants.go` — `tenant.update`
  - **Audit log errors are silently discarded.** All `LogAction` call sites use `_ = h.audit.LogAction(...)` — errors are ignored. This means a database outage or connection issue would silently drop audit entries. For compliance-sensitive deployments, audit write failures should at minimum be logged as errors, and potentially should fail the request (write-ahead audit pattern). Low priority for current threat model.
  - **No audit log retention/rotation policy.** The `audit_log` table grows without bound. For production deployments, operators need guidance on archiving or partitioning by `created_at`. Not a security invariant issue, but an operational concern.

---

## 2. Test Categories

### 2.1 Authentication & Session Management

**API key auth (`server/auth/`)**
- [ ] Verify plaintext API keys are never written to DB, logs, or audit entries. Grep for key material in log output during test runs.
- [ ] Constant-time comparison on key hash lookup — check for timing oracle in `validateAPIKey`.
- [ ] Expired keys rejected (expiry enforcement on every request, not cached past expiry).
- [ ] Revoked keys rejected immediately (no cache TTL > a few seconds, or cache invalidation on revoke).
- [ ] `is_admin=true` bypass: confirm it only applies where intended; confirm it still enforces tenant scope.
- [ ] Key prefix (`sk_`) collision resistance: verify full-length hash comparison, not prefix matching.
- [ ] Scoped key enforcement: scoped key cannot access out-of-scope devices/groups/tags/sites even via indirect endpoints (e.g., listing jobs for a device it cannot see).

**mTLS agent auth (`server/api/checkin.go`, mTLS middleware)**
- [ ] Cert chain validation: present a self-signed cert or one signed by a different CA — rejected.
- [ ] Cert expiry: present a cert `NotAfter` in the past — rejected.
- [ ] Revocation: revoke cert in DB, agent request with it — rejected on the **next** request (no stale cache).
- [ ] Revocation at device level (`devices.status = 'revoked'`) — all requests from that device rejected even with a valid cert.
- [ ] Serial number lookup: confirm fix from commit 5567335 is correct across all code paths (big.Int formatting consistency).
- [ ] `VerifyClientCertIfGiven` vs `RequireAnyClientCert`: confirm enrollment endpoint accepts no cert, all other agent endpoints require one.
- [ ] Cert presented by agent A cannot be used to impersonate agent B (tenant_id + device_id bound to cert, not trusted from request body).

**Enrollment tokens (`server/api/enrollment.go`)**
- [ ] Single-use atomicity: race two concurrent enrollments with the same token — exactly one succeeds. Use a parallel goroutine test.
- [ ] Token hash comparison is constant-time.
- [ ] Expired tokens rejected.
- [ ] Token scope (group/tag/site) is copied to the device at enrollment and cannot be escaped.

**OIDC/SSO**
- [ ] ID token signature verified against JWKS.
- [ ] `aud` and `iss` claims validated.
- [ ] Token expiry enforced.
- [ ] User-to-tenant mapping cannot be spoofed via claims the user controls.

### 2.2 Authorization (RBAC + Scope)

**Predefined role matrix**
- [ ] For each of Super Admin, Tenant Admin, Operator, Technician, Viewer: test a representative set of endpoints (read, write, admin) and confirm the permission matrix in `server/rbac/` matches `SECURITY.md`.
- [ ] Existing `tests/integration/rbac_test.go` — review coverage, add gaps.

**Privilege escalation paths**
- [ ] Can an Operator create an API key with permissions greater than their own? (must not)
- [ ] Can a Tenant Admin create a role with cross-tenant permissions? (must not)
- [ ] Can a user assign themselves a higher role? (must not)
- [ ] Can a scoped key create a job targeting devices outside its scope? (must not)
- [ ] Can a non-admin revoke an admin's key or device? Verify consistent behavior.

**Tenant isolation**
- [ ] Every repository/store method that returns tenant data takes `tenant_id` as a parameter and uses it in the `WHERE` clause. Audit `server/store/` for queries missing this filter.
- [ ] Tenant ID never read from request body/params — only from auth context. Grep for `ctx.Tenant()` vs `req.TenantID`.
- [ ] Integration test: tenant A cannot read/write/list tenant B's devices, jobs, files, users, audit log, enrollment tokens.
- [ ] Integration test: supplying a different tenant's device ID in a path param returns 404, not 403 (don't leak existence).

### 2.3 Input Validation & Injection

**SQL injection**
- [ ] Confirm all DB access goes through pgx parameterized queries; no string concatenation into SQL. Grep `server/store/` for `fmt.Sprintf.*SELECT`, `+` string building in queries.
- [ ] Sort/filter/pagination params: any `ORDER BY` built from user input? (must be whitelist, not passthrough)

**Command injection (agent executor)**
- [ ] `exec` job type: command is passed as `argv`, not `sh -c`, OR if shell is used, parameters are not interpolated from untrusted sources.
- [ ] Verify `agent/executor/` doesn't pass job payloads through a shell in a way that allows escaping.
- [ ] Package install jobs: package names validated against a regex or list; don't allow `--` or shell metacharacters.

**Path traversal**
- [ ] File transfer: verify the file path an agent writes to is server-dictated and sanitized; `../` sequences rejected.
- [ ] Installer hosting endpoints: confirm the file path served is whitelisted, no arbitrary file read.
- [ ] Storage backend: uploaded file paths normalized, no symlink escape.

**Protocol / body parsing**
- [ ] Max body size enforced on all endpoints (DoS protection).
- [ ] Check-in payload: inventory size limits, delta size limits.
- [ ] Chunked file upload: per-chunk and total-size limits enforced; resumable upload cannot exceed declared size.

### 2.4 Cryptography & Key Management

**CA + cert signing**
- [ ] CA private key file permissions: `0600`, owned by service user.
- [ ] CA key never logged.
- [ ] Generated certs have appropriate key usage (`digitalSignature`, `keyEncipherment`), EKU (`clientAuth`).
- [ ] CSR inputs validated: reject CSRs with unexpected SAN, CN, or key usage.
- [ ] ECDSA P-256 enforced for agent keys; RSA / other curves rejected if not intended.

**Artifact signing**
- [ ] Ed25519 public key committed to `keys/release.pub`; verify it matches the key actually used in CI.
- [ ] Verify signature format is what the verifier expects (base64 encoding, byte order).
- [ ] Test negative cases: corrupted signature, wrong key, truncated binary — all rejected.
- [ ] Agent update path: verify signature check is **before** binary is staged, not after.

**Local UI CA (per-device)**
- [ ] Name Constraints: the per-device CA can only sign for `127.0.0.1` / `localhost`. Verify by attempting to sign a cert for a different host using the per-device CA key, confirm the resulting cert would be rejected by a compliant verifier.
- [ ] Per-device CA key stored with restricted permissions.

**Hashing**
- [ ] API key / enrollment token hashing uses SHA-256 with sufficient entropy source (32 bytes from `crypto/rand`, not `math/rand`).
- [ ] No use of MD5 / SHA-1 for anything security-relevant.

**Key rotation procedures**
- [ ] Currently rotation guidance is scattered across multiple docs (release signing, CA, per-device CA, API keys). Consolidate into a single `docs/KEY_ROTATION.md` as a deliverable of this validation pass.
- [ ] Document must cover: intermediate CA rotation, release signing key rotation, per-device local-UI CA regeneration, API key rotation cadence, DB password rotation, and rollback procedure for each.
- [ ] For each key, specify: storage location, rotation trigger (schedule vs. compromise), procedure, blast radius during rotation, and verification step.

### 2.5 Transport Security

- [ ] Server TLS config: minimum TLS 1.2 (prefer 1.3), modern cipher suites only.
- [ ] `passthrough` mode: verify trust of `X-Forwarded-*` headers only from configured proxy IPs, not arbitrary clients.
- [ ] Agent client TLS: verifies server cert chain, does not skip verification (`InsecureSkipVerify=false`). Grep for this.
- [ ] Agent pins server CA (if applicable per `AGENT_AUTH_SPEC.md`) — confirm pin is loaded from a trusted source at install time.
- [ ] DB connection requires TLS in production config (`sslmode=require`).

### 2.6 Agent Security Model

**Poll-only invariant (I1)**
- [ ] Agent binary does not open a listening port except: local UI on 127.0.0.1, local IPC socket/named pipe. Audit `agent/` for `net.Listen` calls.
- [ ] Local UI bind address hard-coded to loopback, not configurable to 0.0.0.0.
- [ ] Local CLI IPC socket permissions: Linux socket mode `0600` or group-restricted; Windows named pipe has matching ACL.

**CDM integrity (I2)**
- [ ] CDM state is stored locally on the agent and the agent refuses to execute jobs when held, regardless of what the server says.
- [ ] Test: server marks a job as ready, agent in CDM hold — job is NOT executed.
- [ ] Test: agent reports CDM hold on check-in, server respects it.
- [ ] CDM grant requires local auth (not a server action).
- [ ] CDM session expiry: in-flight job completes, no new jobs start.

**Agent update integrity**
- [ ] Binary signature verified **before** being written to the install path.
- [ ] Rollback on post-restart failure: confirm previous binary is preserved and restored.
- [ ] Version downgrade policy: downgrade is **only permitted via the rollback path** (previous-binary restore). Any other downgrade request (e.g., server-dispatched update pointing to a lower version) must be rejected by the agent. Verify with a test that installs version N, then dispatches an update to version N-1 via the normal update job — must be refused.

### 2.7 Secrets Hygiene

- [ ] grep the repo for hard-coded credentials, test API keys, test tokens left in source.
- [ ] Confirm env-var-based secrets are not logged on startup (no `log.Printf("config: %+v", cfg)`).
- [ ] Confirm `.env` and key files are in `.gitignore`.
- [ ] Test log output (structured logger) does not include API key headers, cert private keys, or DB password.
- [ ] Error responses do not leak internals (stack traces, DB errors verbatim).

### 2.8 Denial of Service & Resource Limits

- [ ] Rate limiting applied in two tiers: **per-tenant** (higher, generous ceiling for legitimate automation) and **per-IP** (much lower, catches brute-force and unauthenticated floods). Both tiers active simultaneously; whichever triggers first wins.
- [ ] Rate limiting on: enrollment endpoint, login, API key auth attempts, check-in (per-device ceiling separate from per-tenant).
- [ ] Per-IP limit applies **before** authentication so unauthenticated abuse is shed early.
- [ ] Per-tenant resource limits: max devices, max jobs in queue, max file size, max API keys.
- [ ] Agent check-in throttling: malicious agent cannot flood with rapid check-ins.
- [ ] Long-running jobs: agent enforces per-job timeout.
- [ ] Regex / parser DoS: any regex compiled from user input? (jobs targeting filter, tag patterns)

### 2.9 Audit Log Integrity (I10)

- [ ] Verify audit log table has no `UPDATE` or `DELETE` paths in the codebase. Grep `server/audit/` and `server/store/` for writes other than INSERT.
- [ ] DB-level: can the service user `DELETE`/`UPDATE` the audit log? (ideally no — separate grants).
- [ ] Sensitive actions all produce audit entries: role change, API key create/revoke, device revoke, enrollment, job creation, CDM toggle.
- [ ] Audit entries include actor identity + source IP.

### 2.10 Dependency & Supply Chain

- [ ] `go list -m all` + `govulncheck` — report known-vulnerable dependencies.
- [ ] UI: `npm audit` results, pin versions in `package-lock.json`.
- [ ] Docker base images: current and scanned (distroless or alpine with recent patches per plan).
- [ ] CI pipeline: release signing key accessible only to release workflow, protected by environment.

---

## 3. Methodology

### 3.1 Static Analysis

```
make lint                        # golangci-lint baseline
govulncheck ./...                # known CVE scan
gosec ./...                      # Go security linter (add if not present)
semgrep --config=auto            # pattern-based security rules
```

Grep audits for:
- `InsecureSkipVerify`
- `fmt.Sprintf.*(SELECT|INSERT|UPDATE|DELETE)`
- `exec.Command.*sh.*-c`
- `math/rand` in security contexts
- `http.ListenAndServe.*:0.0.0.0` in agent code
- `os.Chmod.*0[67]77`

### 3.2 Dynamic Tests

Add a new integration test file `tests/integration/security_test.go` with tagged subtests for each category above. Reuse the existing harness (`tests/integration/harness_test.go`).

Negative-test patterns:
- Forge requests from one tenant's key against another tenant's resources.
- Replay an enrollment token after it's been consumed.
- Present an expired / revoked / foreign cert to agent endpoints.
- Submit jobs that reference devices outside a scoped key's scope.

### 3.3 Design Review

For each invariant in §1, produce a short finding: **verified / partial / gap / accepted risk**, with the test or code reference that supports it. Collect these into `SEC_VALIDATION_FINDINGS.md` at the end.

---

## 4. Deliverables

1. `SEC_VALIDATION_FINDINGS.md` — one entry per invariant + category, with evidence.
2. `tests/integration/security_test.go` — new negative tests.
3. Any code fixes for gaps found, as separate commits with `sec:` prefix.
4. Updated `SECURITY.md` if design decisions change as a result.
5. `docs/KEY_ROTATION.md` — consolidated key rotation procedures (currently scattered across specs).
6. **Follow-on (post-validation):** `docs/THREAT_MODEL.md` — STRIDE per component. Built *after* this validation pass so the threat model reflects tested reality, not aspiration.

---

## 5. Out of Scope

- External pentest / red team
- Load testing / performance DoS
- Physical security of agent devices
- End-user device compromise (attacker with local root on the endpoint)
- Third-party dependency audit beyond `govulncheck` / `npm audit`
