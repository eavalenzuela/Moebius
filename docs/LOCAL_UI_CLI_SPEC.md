# Local UI & CLI Security Model

---

## Overview

Each agent exposes two local management interfaces:

- **CLI** — communicates with the agent via a Unix socket (Linux) or named pipe (Windows)
- **Local Web UI** — a localhost-only HTTPS web server, intended for end users (particularly for CDM interactions)

Both interfaces share the same authentication mechanism — OS user account credentials — and the same authorization model. Neither interface is accessible remotely under any circumstances.

---

## Interface Exposure

### CLI — Unix Socket / Named Pipe

| Platform | Path |
|---|---|
| Linux | `/run/moebius-agent/moebius-agent.sock` |
| Windows | `\\.\pipe\moebius-agent` |

- Socket/pipe is created by the agent on startup with restrictive permissions
- Linux: socket owned by `root`, mode `0660`, group `agent-users` (or equivalent)
- Windows: named pipe ACL restricted to SYSTEM + local Administrators
- CLI binary communicates exclusively via the socket/pipe — no network involvement
- No TLS required on the socket; OS-level permissions are the transport security boundary

### Local Web UI — Localhost HTTPS

- Binds exclusively to `127.0.0.1` — never `0.0.0.0`
- **Default port:** `57000` (operator-overridable at install time or via config file)
- Accessible at `https://localhost:57000`
- TLS is mandatory — HTTP is not served under any circumstance

---

## HTTPS Certificate Strategy

### Per-Device Local CA

A unique CA is generated on-device at install time. The CA private key never leaves the machine — no shared CA key is bundled in the installer. This eliminates Superfish-class risks where a compromised shared key enables MITM attacks across all installations.

**Install-time steps:**
1. Generate unique per-device CA keypair locally
2. CA certificate is constrained to `localhost` and `127.0.0.1` via X.509 Name Constraints
3. CA certificate is installed into the OS trust store (browser-inherited)
4. Generate initial localhost TLS certificate signed by the local CA
5. Configure local web server with the localhost certificate

**CA properties:**
- Key algorithm: ECDSA P-256
- Validity: 10 years (long-lived; private key stays on device)
- Name Constraints extension: permitted `localhost`, `127.0.0.1` only
- Stored in: OS-protected location (root/SYSTEM access only)

**Localhost cert properties:**
- Key algorithm: ECDSA P-256
- Validity: 90 days
- SANs: `localhost`, `127.0.0.1`
- Rotated automatically by the agent before expiry

### OS Trust Store Installation

| Platform | Method |
|---|---|
| Linux (Debian/Ubuntu) | Copy to `/usr/local/share/ca-certificates/`, run `update-ca-certificates` |
| Linux (RHEL/Fedora) | Copy to `/etc/pki/ca-trust/source/anchors/`, run `update-ca-trust` |
| Windows | Install into `Local Machine\Root` via CertMgr API |

**Firefox note:** Firefox manages its own trust store independently of the OS on some platforms. The installer should additionally install the CA into Firefox's trust store via its `certutil` tooling if Firefox is detected. This is best-effort — a fallback warning in the UI should guide users on manual import if needed.

### Security Properties

- CA private key is unique per device — a compromised key on one machine does not affect any other
- Name Constraints limit the CA to localhost only — it cannot sign trusted certs for any external domain even if extracted
- Short-lived localhost certs limit exposure if the localhost cert itself is compromised
- No shared secret or CA key is ever distributed via the installer

---

## Authentication

Both the CLI and local web UI authenticate using OS user account credentials via platform authentication APIs.

### Mechanism

| Platform | API |
|---|---|
| Linux | PAM (Pluggable Authentication Modules) via `go-pam` |
| Windows | `LogonUser()` in `advapi32.dll` via Go syscall |

The agent process requires sufficient privilege to call these APIs (root on Linux, SYSTEM on Windows — consistent with package management requirements).

### CLI Authentication Flow

1. User invokes a CLI command
2. CLI connects to the Unix socket / named pipe
3. Agent prompts for OS username + password (or CLI accepts `--username`/`--password` flags, or reads from environment for scripting)
4. Agent validates credentials via PAM / `LogonUser`
5. On success, agent issues a short-lived session token (valid: 15 minutes, idle-refreshed)
6. Session token is held in memory by the CLI process for the duration of the session
7. Subsequent CLI commands within the session reuse the token without re-prompting

### Local Web UI Authentication Flow

1. User visits `https://localhost:57000`
2. Agent serves a login page
3. User submits OS username + password via HTTPS POST
4. Agent validates credentials via PAM / `LogonUser`
5. On success, agent issues a session cookie (HttpOnly, Secure, SameSite=Strict)
6. Session is valid for 30 minutes of inactivity, maximum 8 hours absolute
7. Session is invalidated on explicit logout or agent restart

### Session Token Properties

| Property | CLI Token | Web Session Cookie |
|---|---|---|
| Storage | In-memory (CLI process) | HttpOnly cookie |
| Validity | 15 min, idle-refreshed | 30 min inactivity / 8hr absolute |
| Scope | Single CLI session | Browser session |
| Revocation | Process exit | Explicit logout or agent restart |
| Transmission | Over socket/pipe only | Over localhost HTTPS only |

### Authorization

Access to the local interface is granted to any OS user who can successfully authenticate. No additional local role system is implemented — OS-level access control (admin/root membership) is the authorization boundary.

CDM operations (grant, revoke, toggle) are available to any authenticated local user. This is intentional — in the customer-device context, the local user is the party whose consent matters.

---

## Local Interface Capabilities

Both CLI and web UI expose the same set of operations:

| Operation | CLI | Web UI |
|---|---|---|
| View agent status + version | ✓ | ✓ |
| View current config | ✓ | ✓ |
| View CDM state | ✓ | ✓ |
| Toggle CDM on/off | ✓ | ✓ |
| View pending inbound job requests | ✓ | ✓ |
| Grant CDM session (with duration) | ✓ | ✓ |
| Revoke CDM session | ✓ | ✓ |
| View local audit log | ✓ | ✓ |
| View agent logs | ✓ | ✓ |

The web UI is the primary interface for non-technical end users interacting with CDM. The CLI is the primary interface for local administrators and scripted operations.

---

## CDM Interactions via Local Interfaces

### Grant Flow (Web UI)

1. User visits `https://localhost:57000` and authenticates
2. UI displays pending inbound job requests from the server (if any)
3. User selects a session duration (preset options + custom input)
4. User clicks **Grant Access**
5. Agent updates CDM session state locally
6. Agent reports new session state to server on next check-in
7. UI displays active session with countdown timer and **Revoke** button

### Grant Flow (CLI)

```bash
# View pending requests
agent cdm status

# Grant a timed session
agent cdm grant --duration 10m

# Grant with custom expiry
agent cdm grant --duration 1h

# Revoke active session
agent cdm revoke

# Toggle CDM on/off
agent cdm enable
agent cdm disable
```

### Session Revocation

Either interface can revoke an active CDM session at any time. Revocation is immediate and local-authoritative — the server learns of it on the next check-in. In-flight jobs on the agent complete; no new jobs are accepted after revocation.

---

## Audit Logging

All local interface interactions are written to the local audit log, separate from the log shipped to the server:

| Event | Logged Fields |
|---|---|
| Authentication success | timestamp, username, interface (cli/ui), source |
| Authentication failure | timestamp, username, interface, failure reason |
| CDM toggle | timestamp, username, interface, old state, new state |
| CDM session grant | timestamp, username, interface, duration, expires_at |
| CDM session revoke | timestamp, username, interface, revocation reason |
| Config view | timestamp, username, interface |

Local audit log is stored in a root/SYSTEM-only readable file. A filtered view (CDM events only) is surfaced in the web UI and CLI for the local user. Full log is accessible to root/SYSTEM only.

---

## Security Boundaries Summary

| Threat | Mitigation |
|---|---|
| Remote access to local UI | Bound to `127.0.0.1` only; no remote access possible |
| Browser MITM on localhost | Per-device CA + Name Constraints; HTTPS enforced |
| Shared CA key compromise | CA generated on-device; no shared key exists |
| Unauthorized local access | OS credential auth via PAM / LogonUser |
| Session hijacking | HttpOnly + Secure + SameSite=Strict cookies; short session lifetime |
| CDM bypass by server | CDM state is local-authoritative; server cannot override it |
| Privilege escalation via socket | Socket/pipe ACL restricted to admin/root equivalents |
