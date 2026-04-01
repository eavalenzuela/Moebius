#!/usr/bin/env bash
# Moebius Agent — Linux Install Script
# Usage: sudo ./install.sh [options]
#   --enrollment-token <token>   Enrollment token (required for new install)
#   --server-url <url>           Server URL (required for new install)
#   --ca-cert <path>             Path to CA certificate file (optional)
#   --cdm-enabled                Enable Customer Device Mode at install
#   --upgrade                    Upgrade existing installation in-place
#   --binary <path>              Path to agent binary (default: ./moebius-agent)

set -euo pipefail

# --- Constants ---
BINARY_INSTALL_PATH="/usr/local/bin/moebius-agent"
PKG_HELPER_INSTALL_PATH="/usr/local/bin/moebius-pkg-helper"
CONFIG_DIR="/etc/moebius-agent"
DATA_DIR="/var/lib/moebius-agent"
LOG_DIR="/var/log/moebius-agent"
RUNTIME_DIR="/run/moebius-agent"
DROP_DIR="/var/lib/moebius-agent/drop"
SERVICE_NAME="moebius-agent"
SERVICE_USER="moebius-agent"
SERVICE_GROUP="moebius-agent"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# --- Defaults ---
ENROLLMENT_TOKEN=""
SERVER_URL=""
CA_CERT_PATH=""
CDM_ENABLED="false"
UPGRADE=false
BINARY_SRC="./moebius-agent"

# --- Color helpers ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
fatal() { error "$@"; exit 1; }

# --- Parse arguments ---
while [[ $# -gt 0 ]]; do
    case "$1" in
        --enrollment-token) ENROLLMENT_TOKEN="$2"; shift 2 ;;
        --server-url)       SERVER_URL="$2"; shift 2 ;;
        --ca-cert)          CA_CERT_PATH="$2"; shift 2 ;;
        --cdm-enabled)      CDM_ENABLED="true"; shift ;;
        --upgrade)          UPGRADE=true; shift ;;
        --binary)           BINARY_SRC="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: sudo $0 [options]"
            echo ""
            echo "Options:"
            echo "  --enrollment-token <token>   Enrollment token (required for new install)"
            echo "  --server-url <url>           Server URL (required for new install)"
            echo "  --ca-cert <path>             Path to CA certificate file"
            echo "  --cdm-enabled                Enable CDM at install time"
            echo "  --upgrade                    Upgrade existing installation"
            echo "  --binary <path>              Path to agent binary (default: ./moebius-agent)"
            exit 0
            ;;
        *) fatal "Unknown option: $1" ;;
    esac
done

# --- Preflight checks ---

# Must be root.
if [[ $EUID -ne 0 ]]; then
    fatal "This script must be run as root (use sudo)"
fi

# Require systemd.
if ! command -v systemctl &>/dev/null; then
    fatal "systemd is required but not found. This installer only supports systemd-based systems."
fi

if ! pidof systemd &>/dev/null && [[ ! -d /run/systemd/system ]]; then
    fatal "systemd is installed but does not appear to be running as init system."
fi

# Check for existing installation.
EXISTING_INSTALL=false
if [[ -f "$BINARY_INSTALL_PATH" ]]; then
    EXISTING_INSTALL=true
    if [[ "$UPGRADE" != true ]]; then
        info "Existing installation detected at $BINARY_INSTALL_PATH"
        info "Running in upgrade mode."
        UPGRADE=true
    fi
fi

# Validate required arguments for new installs.
if [[ "$UPGRADE" != true ]]; then
    if [[ -z "$ENROLLMENT_TOKEN" ]]; then
        fatal "--enrollment-token is required for new installations"
    fi
    if [[ -z "$SERVER_URL" ]]; then
        fatal "--server-url is required for new installations"
    fi
fi

# Validate binary source exists.
if [[ ! -f "$BINARY_SRC" ]]; then
    fatal "Agent binary not found at: $BINARY_SRC"
fi

if [[ ! -x "$BINARY_SRC" ]]; then
    chmod +x "$BINARY_SRC"
fi

info "Starting Moebius Agent installation..."
if [[ "$UPGRADE" == true ]]; then
    info "Mode: upgrade"
else
    info "Mode: new install"
fi

# --- Step 1: Create system user and group ---
if ! getent group "$SERVICE_GROUP" &>/dev/null; then
    info "Creating system group: $SERVICE_GROUP"
    groupadd --system "$SERVICE_GROUP"
fi

if ! getent passwd "$SERVICE_USER" &>/dev/null; then
    info "Creating system user: $SERVICE_USER"
    useradd --system \
        --gid "$SERVICE_GROUP" \
        --home-dir "$DATA_DIR" \
        --no-create-home \
        --shell /usr/sbin/nologin \
        "$SERVICE_USER"
fi

# --- Step 2: Create directory structure ---
info "Creating directory structure..."

mkdir -p "$CONFIG_DIR"
chown root:"$SERVICE_GROUP" "$CONFIG_DIR"
chmod 0750 "$CONFIG_DIR"

mkdir -p "$DATA_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$DATA_DIR"
chmod 0750 "$DATA_DIR"

mkdir -p "$DROP_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$DROP_DIR"
chmod 0750 "$DROP_DIR"

mkdir -p "$LOG_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$LOG_DIR"
chmod 0750 "$LOG_DIR"

mkdir -p "$RUNTIME_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$RUNTIME_DIR"
chmod 0750 "$RUNTIME_DIR"

# --- Step 3: Install binary ---
if [[ "$EXISTING_INSTALL" == true ]]; then
    info "Backing up existing binary..."
    cp -f "$BINARY_INSTALL_PATH" "${BINARY_INSTALL_PATH}.previous"
    chown root:root "${BINARY_INSTALL_PATH}.previous"
    chmod 0755 "${BINARY_INSTALL_PATH}.previous"
fi

info "Installing binary to $BINARY_INSTALL_PATH"
cp -f "$BINARY_SRC" "$BINARY_INSTALL_PATH"
chown root:root "$BINARY_INSTALL_PATH"
chmod 0755 "$BINARY_INSTALL_PATH"

# Print version.
AGENT_VERSION=$("$BINARY_INSTALL_PATH" version 2>/dev/null || echo "unknown")
info "Installed: $AGENT_VERSION"

# --- Step 3b: Install setuid package helper ---
PKG_HELPER_SRC="./moebius-pkg-helper"
if [[ -f "$PKG_HELPER_SRC" ]]; then
    info "Installing setuid package helper to $PKG_HELPER_INSTALL_PATH"
    cp -f "$PKG_HELPER_SRC" "$PKG_HELPER_INSTALL_PATH"
    chown root:root "$PKG_HELPER_INSTALL_PATH"
    chmod 4755 "$PKG_HELPER_INSTALL_PATH"
else
    warn "Package helper binary not found at $PKG_HELPER_SRC (skipping)"
    warn "Package management jobs will not work without the setuid helper."
fi

# --- Step 4: Write configuration (new install only) ---
CONFIG_FILE="${CONFIG_DIR}/config.toml"

if [[ "$UPGRADE" != true ]] || [[ ! -f "$CONFIG_FILE" ]]; then
    info "Writing configuration to $CONFIG_FILE"
    cat > "$CONFIG_FILE" <<TOML
[server]
url = "${SERVER_URL}"
poll_interval_seconds = 30

[storage]
drop_directory = "${DROP_DIR}"
space_check_enabled = true
space_check_threshold = 0.50

[local_ui]
enabled = true
port = 57000

[logging]
level = "info"
file = "${LOG_DIR}/moebius-agent.log"

[cdm]
enabled = ${CDM_ENABLED}
TOML
    chown root:"$SERVICE_GROUP" "$CONFIG_FILE"
    chmod 0640 "$CONFIG_FILE"
fi

# --- Step 5: Write enrollment token ---
if [[ -n "$ENROLLMENT_TOKEN" ]]; then
    TOKEN_FILE="${CONFIG_DIR}/enrollment.token"
    info "Writing enrollment token"
    printf '%s' "$ENROLLMENT_TOKEN" > "$TOKEN_FILE"
    chown root:"$SERVICE_GROUP" "$TOKEN_FILE"
    chmod 0600 "$TOKEN_FILE"
fi

# --- Step 6: Install CA certificate ---
if [[ -n "$CA_CERT_PATH" ]]; then
    if [[ ! -f "$CA_CERT_PATH" ]]; then
        warn "CA certificate file not found at: $CA_CERT_PATH (skipping)"
    else
        info "Installing CA certificate"
        cp -f "$CA_CERT_PATH" "${CONFIG_DIR}/ca.crt"
        chown root:"$SERVICE_GROUP" "${CONFIG_DIR}/ca.crt"
        chmod 0644 "${CONFIG_DIR}/ca.crt"
    fi
fi

# --- Step 7: Install systemd unit file ---
info "Installing systemd unit file to $UNIT_FILE"
cat > "$UNIT_FILE" <<'UNIT'
[Unit]
Description=Moebius Device Management Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
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
UNIT

chmod 0644 "$UNIT_FILE"

# --- Step 8: Reload systemd and start service ---
info "Reloading systemd daemon..."
systemctl daemon-reload

if [[ "$UPGRADE" == true ]] && systemctl is-active --quiet "$SERVICE_NAME"; then
    info "Restarting agent service..."
    systemctl restart "$SERVICE_NAME"
else
    info "Enabling and starting agent service..."
    systemctl enable --now "$SERVICE_NAME"
fi

# --- Step 9: Wait for first check-in ---
info "Waiting for agent to start (up to 30s)..."

STARTED=false
for i in $(seq 1 30); do
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        STARTED=true
        break
    fi
    sleep 1
done

if [[ "$STARTED" != true ]]; then
    error "Agent service did not start within 30 seconds."
    error "Check logs: journalctl -u $SERVICE_NAME --no-pager -n 50"
    exit 1
fi

# Give the agent a moment to complete enrollment and first check-in.
CHECKIN_OK=false
WAIT_SECS=30
info "Waiting up to ${WAIT_SECS}s for first check-in..."

for i in $(seq 1 "$WAIT_SECS"); do
    # Check if agent_id file exists (written after successful enrollment).
    if [[ -f "${CONFIG_DIR}/agent_id" ]]; then
        CHECKIN_OK=true
        break
    fi
    # Also check if service crashed.
    if ! systemctl is-active --quiet "$SERVICE_NAME"; then
        error "Agent service stopped unexpectedly."
        error "Check logs: journalctl -u $SERVICE_NAME --no-pager -n 50"
        exit 1
    fi
    sleep 1
done

echo ""
if [[ "$CHECKIN_OK" == true ]]; then
    AGENT_ID=$(cat "${CONFIG_DIR}/agent_id" 2>/dev/null || echo "unknown")
    info "Installation successful!"
    info "Agent ID: $AGENT_ID"
    info "Service:  systemctl status $SERVICE_NAME"
    info "Logs:     journalctl -u $SERVICE_NAME -f"
else
    if [[ "$UPGRADE" == true ]]; then
        # Upgrades may already be enrolled; agent_id might already exist.
        if [[ -f "${CONFIG_DIR}/agent_id" ]]; then
            info "Upgrade successful! Agent is running."
        else
            warn "Agent is running but enrollment has not completed within ${WAIT_SECS}s."
            warn "This may be normal if the server is unreachable."
            warn "Check status: systemctl status $SERVICE_NAME"
        fi
    else
        warn "Agent is running but enrollment has not completed within ${WAIT_SECS}s."
        warn "This may be normal if the server is unreachable."
        warn "Check status: systemctl status $SERVICE_NAME"
    fi
fi

exit 0
