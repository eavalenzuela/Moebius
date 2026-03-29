# Agent Installer & Packaging Specification

---

## Overview

The agent is distributed as a binary tarball (Linux) and MSI installer (Windows). Installers are hosted on the management server itself, making the server the single distribution point for all agent artifacts. This supports air-gapped and corporate-network-restricted environments without requiring access to external package registries or GitHub.

All installer artifacts are verified using SHA-256 checksum and Ed25519 signature, consistent with the file transfer verification model.

---

## Supported Platforms & Formats

| OS | Format | Architectures |
|---|---|---|
| Linux | `.tar.gz` tarball | `amd64`, `arm64` |
| Windows | `.msi` installer | `amd64` |

### Linux Tarball Contents

```
agent-linux-amd64-1.5.0.tar.gz
├── agent                    ← agent binary
├── install.sh               ← install script
├── uninstall.sh             ← uninstall script
├── agent.service            ← systemd unit file
└── README.md
```

### Windows MSI Contents

The MSI packages:
- Agent binary (`agent.exe`)
- Windows Service registration
- Install/uninstall logic (via MSI custom actions)
- WiX-based installer (built with WiX Toolset v4)

---

## Installer Hosting

Installers are served directly from the management server API:

```
GET /v1/installers                          ← list available installers
GET /v1/installers/{os}/{arch}/{version}    ← download specific version
GET /v1/installers/{os}/{arch}/latest       ← download latest stable
GET /v1/installers/{os}/{arch}/{version}/checksum   ← SHA-256 checksum file
GET /v1/installers/{os}/{arch}/{version}/signature  ← Ed25519 signature file
```

These endpoints require a valid API key or enrollment token — installers are not publicly accessible without authentication.

### Installer Metadata Response (`GET /v1/installers`)

```json
{
  "installers": [
    {
      "os": "linux",
      "arch": "amd64",
      "version": "1.5.0",
      "channel": "stable",
      "size_bytes": 20971520,
      "sha256": "e3b0c44298fc1c149afb...",
      "signature_key_id": "sigkey_01HZ...",
      "download_url": "/v1/installers/linux/amd64/1.5.0",
      "released_at": "2026-03-28T12:00:00Z"
    }
  ]
}
```

### Server-Side Storage

Installer artifacts are stored in the same storage backend as file transfers (local filesystem or S3-compatible). They are uploaded to the server as part of the release process using a dedicated admin API endpoint:

```
POST /v1/installers
Authorization: Bearer <admin_api_key>
Content-Type: application/json
```

```json
{
  "version": "1.5.0",
  "channel": "stable",
  "os": "linux",
  "arch": "amd64",
  "file_id": "fil_01HZ...",
  "sha256": "e3b0c44298fc1c149afb...",
  "signature": "<base64-encoded Ed25519 signature>",
  "signature_key_id": "sigkey_01HZ..."
}
```

The release process uploads installer artifacts via the standard chunked file upload API, then registers them as installable versions via this endpoint.

---

## Server-Generated Enrollment Command

The server generates a one-line install command per enrollment token, scoped to the target device's OS and architecture. Operators copy this command and run it on the target device.

### Generating an Enrollment Command

Via web UI: **Devices → Add Device → Select OS/Arch → Copy install command**

Via API:
```
POST /v1/enrollment-tokens/{token_id}/install-command
```

```json
{
  "os": "linux",
  "arch": "amd64"
}
```

**Response:**
```json
{
  "command": "curl -fsSL https://manage.example.com/v1/install/linux/amd64?token=enr_01HZ_<secret> | sudo bash",
  "expires_at": "2026-03-29T12:00:00Z",
  "token_id": "enr_01HZ..."
}
```

### Install Endpoint

```
GET /v1/install/{os}/{arch}?token=<enrollment_token>
```

This endpoint returns a shell script (Linux) or PowerShell script (Windows) that:
1. Downloads the latest stable agent installer for the specified platform
2. Verifies the SHA-256 checksum
3. Verifies the Ed25519 signature
4. Runs the installer with the enrollment token pre-configured
5. Starts the agent service

The enrollment token is embedded in the install script at generation time — it is not passed as a command-line argument to the installer, preventing it from appearing in process listings.

### Security Note on curl | bash

The install script is served over HTTPS from the management server. The enrollment token in the URL is single-use and time-limited. Operators who are uncomfortable with the curl | bash pattern can instead:
1. Download the installer tarball/MSI separately
2. Verify it manually
3. Run the installer with `--enrollment-token` flag

Both paths are fully supported.

---

## Linux Installation

### Install Script Behaviour (`install.sh`)

1. Detects init system (systemd required; exits with error if not found)
2. Checks for existing agent installation — upgrades in place if found
3. Creates dedicated system user and group: `moebius-agent:moebius-agent`
4. Installs binary to `/usr/local/bin/moebius-agent` (mode `0755`, owned `root:root`)
5. Creates directory structure (see below)
6. Writes enrollment token to `/etc/moebius-agent/enrollment.token` (mode `0600`, owned `root:moebius-agent`)
7. Writes server URL and CA certificate to `/etc/moebius-agent/config.toml`
8. Installs systemd unit file to `/etc/systemd/system/moebius-agent.service`
9. Runs `systemctl daemon-reload`
10. Enables and starts the agent service
11. Waits up to 30 seconds for first successful check-in
12. Reports success or failure to the operator

### Directory Structure (Linux)

```
/usr/local/bin/moebius-agent              ← agent binary
/usr/local/bin/moebius-agent.previous     ← previous binary (post-update)
/etc/moebius-agent/
  config.toml                     ← agent configuration (root:moebius-agent, 0640)
  enrollment.token                ← enrollment token, consumed on first run (root:moebius-agent, 0600)
  ca.crt                          ← server CA certificate (root:moebius-agent, 0644)
  client.crt                      ← agent client certificate (root:moebius-agent, 0640)
  client.key                      ← agent private key (root:moebius-agent, 0600)
/var/lib/moebius-agent/
  pending_update.json             ← update verification file (root:moebius-agent, 0640)
  drop/                           ← file transfer drop directory (root:moebius-agent, 0750)
/var/log/moebius-agent/
  moebius-agent.log               ← agent log file (root:moebius-agent, 0640)
/run/moebius-agent/
  moebius-agent.sock              ← Unix socket for CLI (root:moebius-agent, 0660)
```

### systemd Unit File

```ini
[Unit]
Description=Moebius Device Management Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=moebius-agent
Group=moebius-agent
ExecStart=/usr/local/bin/moebius-agent run
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=10s
TimeoutStartSec=30s

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/moebius-agent /var/log/moebius-agent /run/moebius-agent /etc/moebius-agent
PrivateTmp=yes
PrivateDevices=yes
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

Note: Package management operations (apt, dnf, etc.) require elevated privilege. The agent drops to the `moebius-agent` user for most operations but uses a minimal setuid helper binary for package manager invocations. This is preferable to running the entire agent as root.

### Linux Configuration File (`/etc/moebius-agent/config.toml`)

```toml
[server]
url = "https://manage.example.com"
poll_interval_seconds = 30

[storage]
drop_directory = "/var/lib/moebius-agent/drop"
space_check_enabled = true
space_check_threshold = 0.50

[local_ui]
enabled = true
port = 57000

[logging]
level = "info"
file = "/var/log/moebius-agent/moebius-agent.log"

[cdm]
enabled = false  # set to true to enable Customer Device Mode at install time
```

---

## Windows Installation

### MSI Installer Behaviour

1. Checks for existing agent installation — upgrades in place if found
2. Creates local service account: `NT SERVICE\MoebiusAgent`
3. Installs binary to `C:\Program Files\MoebiusAgent\moebius-agent.exe`
4. Creates directory structure (see below)
5. Writes enrollment token to `C:\ProgramData\MoebiusAgent\enrollment.token` (ACL: SYSTEM + Administrators only)
6. Writes server URL and CA certificate to `C:\ProgramData\MoebiusAgent\config.toml`
7. Installs CA certificate into `Local Machine\Root` certificate store
8. Registers Windows Service (`MoebiusAgent`) with automatic start
9. Starts the service
10. Waits up to 30 seconds for first successful check-in
11. Reports success or failure via MSI installer UI or exit code

### MSI Installation Flags

```
msiexec /i agent-windows-amd64-1.5.0.msi /quiet ^
  ENROLLMENT_TOKEN=enr_01HZ_<secret> ^
  SERVER_URL=https://manage.example.com ^
  CDM_ENABLED=0
```

All flags can also be set interactively via the MSI UI for manual installations.

### Directory Structure (Windows)

```
C:\Program Files\MoebiusAgent\
  moebius-agent.exe               ← agent binary (SYSTEM + Administrators: RX)
  moebius-agent.previous.exe      ← previous binary (post-update)

C:\ProgramData\MoebiusAgent\
  config.toml                     ← agent configuration (SYSTEM + Administrators: RW)
  enrollment.token                ← enrollment token, consumed on first run (SYSTEM only: RW)
  ca.crt                          ← server CA certificate
  client.crt                      ← agent client certificate (SYSTEM only: RW)
  client.key                      ← agent private key (SYSTEM only: RW)
  pending_update.json             ← update verification file
  drop\                           ← file transfer drop directory

\\.\pipe\moebius-agent            ← named pipe for CLI (SYSTEM + Administrators)
```

### Windows Service Configuration

- Service name: `MoebiusAgent`
- Display name: `Moebius Device Management Agent`
- Start type: Automatic
- Recovery actions: Restart on failure (1st, 2nd, subsequent) with 10s delay
- Runs as: `NT SERVICE\MoebiusAgent` (minimal privilege service account)

### Windows Configuration File (`C:\ProgramData\MoebiusAgent\config.toml`)

```toml
[server]
url = "https://manage.example.com"
poll_interval_seconds = 30

[storage]
drop_directory = "C:\\ProgramData\\MoebiusAgent\\drop"
space_check_enabled = true
space_check_threshold = 0.50

[local_ui]
enabled = true
port = 57000

[logging]
level = "info"

[cdm]
enabled = false
```

---

## Installer Verification

Before executing any installer, the install script verifies:

### 1. SHA-256 Checksum

```bash
# Linux
sha256sum -c agent-linux-amd64-1.5.0.tar.gz.sha256

# Windows (PowerShell)
(Get-FileHash agent-windows-amd64-1.5.0.msi -Algorithm SHA256).Hash
```

### 2. Ed25519 Signature

The install script uses the agent binary's built-in verify command (bootstrapped from a minimal verification binary bundled in the install script itself):

```bash
agent-verify --key <embedded-public-key> \
             --sig agent-linux-amd64-1.5.0.tar.gz.sig \
             agent-linux-amd64-1.5.0.tar.gz
```

The Ed25519 public key is embedded in the install script at generation time, sourced from the server's registered signing keys. Both checks must pass before the installer is executed — failure causes the script to exit with a non-zero code and an explicit error message.

---

## Uninstall

### Linux

```bash
# Soft uninstall (default) — removes binary and service, retains config and certs
sudo /usr/local/bin/agent uninstall

# Full purge — removes everything including config, certs, logs, and drop directory
sudo /usr/local/bin/agent uninstall --purge
```

**Soft uninstall:**
1. Stops and disables the systemd service
2. Removes systemd unit file; runs `systemctl daemon-reload`
3. Removes agent binary (`/usr/local/bin/moebius-agent`, `moebius-agent.previous`)
4. Removes Unix socket
5. Removes `moebius-agent` system user and group
6. Retains: `/etc/moebius-agent/`, `/var/lib/moebius-agent/`, `/var/log/moebius-agent/`

**Purge (adds to soft uninstall):**
1. Removes `/etc/moebius-agent/` (including certs and config)
2. Removes `/var/lib/moebius-agent/` (including drop directory and its contents)
3. Removes `/var/log/moebius-agent/`

### Windows

```powershell
# Soft uninstall via MSI
msiexec /x agent-windows-amd64-1.5.0.msi /quiet

# Full purge
msiexec /x agent-windows-amd64-1.5.0.msi /quiet PURGE=1
```

**Soft uninstall:**
1. Stops and removes Windows Service
2. Removes agent binary from `Program Files`
3. Removes named pipe
4. Retains: `C:\ProgramData\MoebiusAgent\`

**Purge (adds to soft uninstall):**
1. Removes `C:\ProgramData\MoebiusAgent\` entirely
2. Removes CA certificate from `Local Machine\Root` certificate store

### Server-Side on Uninstall

Uninstall does not automatically revoke the agent's certificate or notify the server. The device will appear offline after the next missed check-in window. Operators should revoke the device via the server UI or API after uninstalling to prevent the device record and certificate from lingering.

---

## CLI Reference

The agent binary serves as both the daemon and the local CLI:

```bash
# Run as daemon (called by service manager)
agent run

# Local management CLI (requires active service, talks via socket/pipe)
agent status                        ← show agent status and config
agent cdm status                    ← show CDM state and pending jobs
agent cdm enable                    ← enable Customer Device Mode
agent cdm disable                   ← disable Customer Device Mode
agent cdm grant --duration 10m      ← grant a timed CDM session
agent cdm revoke                    ← revoke active CDM session
agent logs [--tail 100]             ← view local agent logs
agent version                       ← show agent version

# Installer operations (called by install/uninstall scripts)
agent install --enrollment-token <token> --server-url <url>
agent uninstall [--purge]

# Verification (used during installer verification)
agent verify --key <pubkey> --sig <sigfile> <file>
```

---

## Build & Release Pipeline

### Build Targets

```makefile
# Linux
GOOS=linux  GOARCH=amd64  go build -o dist/agent-linux-amd64   ./agent/cmd/agent
GOOS=linux  GOARCH=arm64  go build -o dist/agent-linux-arm64   ./agent/cmd/agent

# Windows
GOOS=windows GOARCH=amd64 go build -o dist/agent-windows-amd64.exe ./agent/cmd/agent
```

### Release Artifacts

For each release, GitHub Actions produces:

```
dist/
├── agent-linux-amd64-{version}.tar.gz
├── agent-linux-amd64-{version}.tar.gz.sha256
├── agent-linux-amd64-{version}.tar.gz.sig
├── agent-linux-arm64-{version}.tar.gz
├── agent-linux-arm64-{version}.tar.gz.sha256
├── agent-linux-arm64-{version}.tar.gz.sig
├── agent-windows-amd64-{version}.msi
├── agent-windows-amd64-{version}.msi.sha256
└── agent-windows-amd64-{version}.msi.sig
```

### Signing

All artifacts are signed with the project's Ed25519 release signing key. The private key is stored in GitHub Actions secrets and is never committed to the repository. The corresponding public key is:
- Embedded in install scripts at generation time
- Registered as a signing key on the management server
- Published in the project repository (`/keys/release.pub`) for independent verification

### GitHub Actions Release Workflow

```
on: push (tags: v*)

jobs:
  build:
    matrix: [linux/amd64, linux/arm64, windows/amd64]
    steps:
      - checkout
      - setup-go
      - build binary
      - package (tar.gz or MSI)
      - compute SHA-256
      - sign with Ed25519 release key
      - upload artifacts

  publish:
    needs: build
    steps:
      - download all artifacts
      - upload to management server via admin API
      - register as new agent version (stable channel)
      - trigger gradual rollout (if auto-update enabled)
```

---

## Database Additions

```sql
installers (
  id               uuid primary key,
  version          text not null,
  channel          text not null,
  os               text not null,
  arch             text not null,
  file_id          uuid not null references files(id),
  sha256           text not null,
  signature        text not null,
  signature_key_id uuid not null references signing_keys(id),
  released_at      timestamptz not null,
  yanked           bool not null default false,
  yank_reason      text,
  unique (version, os, arch)
)
```

---

## Security Considerations

- **Enrollment token not exposed in process listings** — embedded in the install script, not passed as a CLI argument
- **Installer verification before execution** — checksum and signature both verified before any installer code runs
- **Private key never leaves the device** — agent generates its keypair during enrollment; the installer only carries the enrollment token
- **Minimal service account privilege** — agent runs as a dedicated low-privilege user/service account; package management uses a minimal setuid helper rather than running the full agent as root
- **Soft uninstall default** — retaining config and certs on uninstall allows reinstallation without re-enrollment, but operators should revoke via the server when decommissioning a device
- **CA certificate installed at machine scope** — constrained to localhost only (Name Constraints) per `LOCAL_UI_CLI_SPEC.md`; does not affect trust for external domains
