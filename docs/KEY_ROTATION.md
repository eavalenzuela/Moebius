# Key Rotation Procedures

This document is the single canonical reference for rotating every long-lived
secret in a Moebius deployment. It exists because rotation guidance had been
scattered across `SECURITY.md`, `AGENT_AUTH_SPEC.md`, `LOCAL_UI_CLI_SPEC.md`,
and tribal knowledge — operators had no one place to look when responding to
either a scheduled rotation window or a suspected compromise.

For every key listed below the entry covers:

- **Storage** — where the material lives on disk, in a secret manager, or in
  CI.
- **Trigger** — what causes a rotation: a calendar (planned) and a compromise
  signal (unplanned).
- **Procedure** — the exact commands or actions an operator runs.
- **Blast radius during rotation** — what stops working, and for how long,
  while the rotation is in progress.
- **Verification** — how to confirm the rotation actually took effect, not
  just that the commands ran without errors.
- **Rollback** — how to recover if the rotation goes wrong.

If you are following this doc during an incident, jump straight to the
relevant section. The order below is roughly "highest blast radius first"
so the tabletop reads top-down.

---

## 1. Root CA

The root CA is the trust anchor for the entire mTLS fabric. It signs the
intermediate CA, which in turn signs every agent client certificate. A root
compromise invalidates every agent cert in the fleet.

- **Storage:** `keys/root-ca.crt` and `keys/root-ca.key`. The key file is
  `0o600`, owned by the service user. In production this material should
  live offline (HSM, air-gapped machine, sealed envelope) and only be
  brought online to sign a new intermediate. The root cert is valid for
  ~10 years (`pki.GenerateCA` with `isRoot=true`).
- **Trigger — planned:** Once per ~10 years, well before `NotAfter`.
- **Trigger — compromise:** Any sign that the root key has been read by an
  unauthorized party. This is the worst case — it forces a fleet-wide
  re-enrollment.
- **Procedure:**
  1. On the offline signing machine, generate a fresh root + intermediate
     pair: `moebius-api generate-ca <out-dir>`. This writes
     `root-ca.{crt,key}` and `intermediate-ca.{crt,key}` and prints the
     paths.
  2. Distribute the new `root-ca.crt` to every agent installer image and
     every existing agent (push as part of the agent update payload, or
     out-of-band file copy). Agents must trust both the old and new root
     for the duration of the cutover.
  3. Update the server's `CA_CERT_PATH` / `CA_KEY_PATH` env vars to point
     at the new intermediate.
  4. Restart the API server. Existing agent certs (signed by the old
     intermediate) remain valid until they expire and renew naturally
     against the new intermediate.
  5. Once every agent has renewed at least once (worst case: 90 days from
     the cutover, see Agent Certificates), retire the old root by
     removing it from the agent trust bundle on the next agent update.
- **Blast radius:** Approximately zero downtime if the cutover is staged
  correctly: agents trust both roots simultaneously. Skipping step 2 (pushing
  the new root before flipping the server) bricks the entire fleet — agents
  will fail to verify the server's intermediate and refuse to check in.
- **Verification:**
  - On the server: `openssl x509 -in /path/to/intermediate-ca.crt -noout
    -issuer` reports the new root subject.
  - On a known-good agent: trigger a check-in and watch the server log for
    `agent enrolled` or `checkin received` against the new intermediate's
    serial.
  - Run the existing integration test `TestEnrollment_FullFlow` against the
    new CA.
- **Rollback:** Keep the previous `root-ca.{crt,key}` and
  `intermediate-ca.{crt,key}` in a sealed off-site copy. To roll back,
  point `CA_CERT_PATH`/`CA_KEY_PATH` back at the old intermediate and
  restart the API server. The agent trust bundle still contains the old
  root from before the rotation, so existing agents reconnect immediately.

## 2. Intermediate CA

The intermediate CA is the cert the API server actually loads at startup
and uses to sign agent CSRs. It is rotated on a much shorter cadence than
the root.

- **Storage:** `keys/intermediate-ca.crt` and `keys/intermediate-ca.key`,
  paths supplied via `CA_CERT_PATH` and `CA_KEY_PATH`. In Helm/Kubernetes
  the cert and key are mounted from a Kubernetes Secret. In Docker Compose
  they are bind-mounted from the host. Key file is `0o600` (enforced by
  the local-CA tests via `os.FileMode` checks).
- **Trigger — planned:** Annually. The intermediate cert is issued for
  ~1 year by `pki.GenerateCA` with `isRoot=false`, so a yearly rotation
  matches its natural lifetime.
- **Trigger — compromise:** Suspected read of the intermediate key
  material. Intermediate compromise is recoverable without re-enrolling
  agents *if* the agent client certs themselves were not compromised — you
  rotate the intermediate, agents renew through the new intermediate at
  their next renewal cycle, and the old intermediate is revoked.
- **Procedure:**
  1. On the (offline) root-signing machine, generate a new intermediate
     signed by the existing root:
     ```sh
     moebius-api generate-ca <out-dir>
     ```
     (Note: the current `generate-ca` subcommand always re-generates the
     root too. To rotate *only* the intermediate, call `pki.GenerateCA`
     directly with `isRoot=false` and `parent=<existing root CA>` from a
     short Go program, or extend the subcommand with a `--keep-root` flag
     in a follow-up PR.)
  2. Replace the contents of the Kubernetes Secret (or bind-mounted file)
     holding the intermediate cert + key with the new pair.
  3. Rolling-restart the API server pods. The old intermediate cert
     remains valid for the agents that were issued by it (they verify
     against the root, not the intermediate's identity directly), so
     no agent disruption.
  4. Wait for one full renewal window (default 30 days, see
     `agent/renewal/renewal.go:RenewalThreshold`) so every agent has
     migrated to the new intermediate.
  5. Revoke the old intermediate's serial in the server's revocation list
     (currently no CRL — track in the database `agent_certificates` table
     by deleting the old entries).
- **Blast radius:** Zero agent downtime during the rotation itself —
  agents are signed by the old intermediate, server presents the new
  intermediate, and Go's `tls.Config` validates client certs against the
  shared root. The window where both intermediates coexist is by design.
- **Verification:**
  - `curl -k https://api/health` followed by inspecting the server cert
    chain shows the new intermediate as the issuer of the server cert
    (if you also rotate the server TLS cert in the same cycle).
  - `SELECT count(*) FROM agent_certificates WHERE issuer_serial = '<old>'`
    decreasing over the renewal window confirms agents are migrating.
- **Rollback:** Restore the previous Secret contents and rolling-restart
  the API server. Agents continue to function because the intermediate
  was already valid for them.

## 3. Agent Client Certificates

These are the per-device certs issued during enrollment. They authenticate
the agent to the server on every check-in.

- **Storage:** On the agent host. Linux: under the agent's data directory
  (typically `/var/lib/moebius/agent/`); Windows: `%PROGRAMDATA%\Moebius\agent\`.
  Key file is `0o600`, owned by the agent service user.
- **Trigger — planned:** Automatic. The agent renewal loop
  (`agent/renewal/renewal.go`) checks the cert's `NotAfter` once per
  startup and once per check-in interval; when less than `RenewalThreshold`
  (30 days) remains it generates a new keypair, submits a CSR, and atomically
  swaps in the new cert. Default cert validity is 90 days
  (`defaultCertValidity` in `server/api/enroll.go`), so each agent rotates
  roughly every 60 days under normal operation.
- **Trigger — compromise:** Operator marks the device compromised in the
  UI / API, which revokes the cert and removes it from
  `agent_certificates`. The next check-in fails mTLS and the agent must be
  re-enrolled.
- **Procedure (compromise / forced rotation):**
  1. `DELETE FROM agent_certificates WHERE device_id = '<id>'` (or use
     the equivalent API endpoint when one is added).
  2. On the agent host, restart the agent service with a fresh enrollment
     token: `moebius-agent run --enrollment-token <token>`.
  3. The agent generates a new ECDSA P-256 keypair (the only key type
     accepted by `pki.SignCSR`'s `validateAgentPublicKey` allowlist),
     submits a CSR, and receives a fresh client cert.
- **Blast radius:** A single device is offline for the duration of one
  re-enrollment (~seconds). Fleet-wide forced rotation only happens if
  the intermediate is compromised, in which case follow §2.
- **Verification:**
  - Agent log shows `certificate renewed, new expiry: <date>`.
  - `SELECT expires_at FROM agent_certificates WHERE device_id = '<id>'`
    reflects the new expiry.
- **Rollback:** Not applicable — the agent atomically swaps the cert on
  disk after a successful renewal handshake. If the new cert fails to
  load, the agent keeps using the old one until it expires.

## 4. Per-Device Local UI CA

Each agent generates its own self-signed CA the first time the local web
UI starts. This CA only signs `localhost` / `127.0.0.1` certs and is
constrained by X.509 Name Constraints so a leaked key cannot mint certs
for any other host.

- **Storage:** Agent local data directory, alongside the client cert.
  Permissions `0o600`, enforced by `TestLocalCAGeneration` (`agent/localui`).
- **Trigger — planned:** Annually, or whenever the local UI cert is about
  to expire (90 days, auto-rotated by the agent — see
  `LOCAL_UI_CLI_SPEC.md`).
- **Trigger — compromise:** Local user account compromise on the agent
  host. Because Name Constraints pin the CA to `localhost`, the blast
  radius is limited to the single device — a stolen per-device CA cannot
  be used to MITM the management server or any other agent.
- **Procedure:**
  1. Stop the agent service (`systemctl stop moebius-agent` /
     `Stop-Service moebius-agent`).
  2. Delete the local CA files in the agent data directory.
  3. Start the agent. `agent/localui/localca.go` regenerates the CA on
     first launch. The new CA cert is then re-installed into the OS
     trust store via `truststore_linux.go` / `truststore_windows.go`.
- **Blast radius:** Local UI is unavailable for the duration of the agent
  restart (~seconds). The browser will warn once until the new CA is
  trusted.
- **Verification:**
  - `openssl x509 -in <local-ca.crt> -noout -dates` shows a fresh
    `notBefore`.
  - Browser loads `https://localhost:<port>/` without a trust warning.
- **Rollback:** Restore the previous CA files from a filesystem snapshot.
  No central state to undo.

## 5. Release Signing Key (Ed25519)

The release signing key signs every agent binary and tarball published by
the build pipeline. The agent verifies the Ed25519 signature *before*
writing the new binary to its install path (see `agent/update/update.go`).

- **Storage:**
  - Public key: `keys/release.pub`, base64-encoded 32 bytes, committed to
    the repo and embedded in the agent at build time.
  - Private key: GitHub Actions secret `RELEASE_SIGNING_KEY`, base64
    encoding of the raw 64-byte Ed25519 private key. **Never** committed.
- **Trigger — planned:** Annually, or when rotating signing infrastructure.
- **Trigger — compromise:** Any indication the GitHub Actions secret has
  been read or exfiltrated. Treat any leak of CI logs containing the
  secret as a compromise.
- **Procedure:**
  1. Generate a new keypair locally:
     ```sh
     go run ./tools/keygen -out-pub keys/release.pub -out-priv release.key
     ```
  2. `base64 < release.key | tr -d '\n'` and update the GitHub Actions
     `RELEASE_SIGNING_KEY` secret with the new value.
  3. Securely delete `release.key` from the local machine
     (`shred -u release.key`).
  4. Open a PR replacing `keys/release.pub` with the new public key.
     Merge after CI passes.
  5. Cut a new release. The new binary will be signed by the new key and
     verified by agents that have updated to a build embedding the new
     `keys/release.pub`.
  6. **Critical ordering:** the agent embeds the public key at build
     time, so an agent built before the rotation will fail to verify
     binaries signed with the new key. Stage the rotation across two
     releases: release N adds the new key as a *secondary* trust anchor
     (multi-key support is a follow-up — track separately), release N+1
     removes the old key.
- **Blast radius:** If staged correctly, zero. If the new public key is
  shipped without a transition release, every existing agent will refuse
  the next update — they will keep running their current version
  indefinitely (the agent never falls back to unsigned updates) and need
  manual intervention.
- **Verification:**
  - `openssl pkey -in release.key -noout -text` (locally, before deletion)
    shows the new fingerprint.
  - Cut a test release, run `TestExecuteAgentUpdate_FullFlow` against an
    agent build embedding the new public key — must pass.
  - Cut the same release again with a deliberately corrupted signature
    — `TestExecuteAgentUpdate_ChecksumMismatch` confirms the negative
    path. Both tests already exist in `agent/update`.
- **Rollback:** Keep the old GitHub Actions secret and the old
  `keys/release.pub` archived for one release cycle. To roll back:
  restore the old secret, revert the `keys/release.pub` PR, and re-cut
  the release. Agents that had already updated to the new-key build
  will need a forced re-install with the old build.

## 6. API Keys (Operator / Service)

API keys authenticate human operators and machine-to-machine integrations
to the REST API. They are stored as SHA-256 hashes (`server/auth/apikey.go`)
and never persisted in plaintext after creation.

- **Storage:** Customer-side: in the operator's password manager or the
  integration's secret store. Server-side: only the SHA-256 hash in the
  `api_keys` table.
- **Trigger — planned:** Quarterly for human operator keys. Annually for
  long-lived service integration keys. Tighter for high-privilege admin
  keys.
- **Trigger — compromise:** Any of: key visible in a chat log, screenshot,
  CI log, error report, or third-party breach involving the integration
  that holds the key.
- **Procedure:**
  1. Create the new key via `POST /v1/api-keys` with the same role and
     scope as the old one. Capture the `sk_…` value displayed once.
  2. Roll the new key into the consumer (script, CI secret, integration
     config). Verify the consumer is using the new key by checking the
     server audit log for entries against the new key ID.
  3. Delete the old key via `DELETE /v1/api-keys/{id}`. The
     `apikeys.Delete` handler refuses to delete an admin key unless the
     caller is also an admin (see the recent `admin_key_protected`
     check), so admin-key rotation must be performed by another admin
     key.
- **Blast radius:** Zero if the new key is rolled in before the old one is
  revoked. If the order is reversed, the consumer fails immediately with
  `401 unauthorized`.
- **Verification:**
  - `SELECT count(*) FROM api_keys WHERE id = '<old_id>'` returns 0.
  - Audit log shows recent activity from the new key, none from the old.
- **Rollback:** API keys cannot be un-revoked — the hash is gone. If a
  rotation is mis-ordered and the consumer breaks, create another new
  key and re-roll. Maintain a 24-hour overlap window for non-trivial
  integrations to make recovery straightforward.

## 7. Database Password

Postgres credentials used by the API server, scheduler, and migration
runner.

- **Storage:** Provided via env var (`DATABASE_URL` / `DB_PASSWORD`) from
  a Kubernetes Secret in Helm or from `.env` in Docker Compose. Never
  committed.
- **Trigger — planned:** Annually, or per the operator's compliance
  schedule.
- **Trigger — compromise:** Cluster-level secret leak or any query
  appearing in an unauthorized log.
- **Procedure:**
  1. In Postgres: `ALTER USER moebius WITH PASSWORD '<new>'`. Use a
     password generator with at least 32 chars of `crypto/rand` entropy.
  2. Update the Kubernetes Secret (`kubectl create secret generic ...
     --dry-run=client -o yaml | kubectl apply -f -`) or the
     `.env` file with the new value.
  3. Rolling-restart the API server and scheduler pods. Postgres still
     accepts the old password until the session pool drains, so the
     restart is the actual cutover.
- **Blast radius:** Brief — the rolling restart causes a few seconds of
  request failures per pod. The scheduler missing one cron tick is
  acceptable.
- **Verification:**
  - `kubectl logs deploy/moebius-api` shows successful startup against
    the new password.
  - `psql -U moebius -h <host> -W` with the new password connects.
- **Rollback:** Re-set the database password to the prior value (you must
  have it stashed before the rotation) and re-apply the Secret. This is
  the *only* rotation in this list that absolutely requires an out-of-band
  copy of the prior credential — there is no archival path otherwise.

---

## Inventory Summary

| Key                       | Storage                          | Cadence    | Section |
| ------------------------- | -------------------------------- | ---------- | ------- |
| Root CA                   | Offline / sealed                 | ~10 years  | §1      |
| Intermediate CA           | `CA_CERT_PATH`/`CA_KEY_PATH`     | Annually   | §2      |
| Agent client certs        | Per-agent disk                   | Auto, 60d  | §3      |
| Per-device local UI CA    | Per-agent disk                   | Annually   | §4      |
| Release signing key       | GitHub Actions secret            | Annually   | §5      |
| API keys                  | Hash-only in `api_keys` table    | Quarterly  | §6      |
| Database password         | K8s Secret / `.env`              | Annually   | §7      |

When responding to a compromise, also revoke any derived secrets: a
compromised intermediate CA implies treating all currently-issued agent
certs as potentially mis-trusted, and a compromised API key with admin
scope implies a full audit-log review for the key's lifetime.
