#!/usr/bin/env bash
# Moebius Agent — Linux Uninstall Script
# Usage: sudo ./uninstall.sh [--purge]
#   --purge   Remove all config, data, and logs (default: retain them)

set -euo pipefail

BINARY="/usr/local/bin/moebius-agent"
PURGE=false

for arg in "$@"; do
    case "$arg" in
        --purge) PURGE=true ;;
        -h|--help)
            echo "Usage: sudo $0 [--purge]"
            echo ""
            echo "  --purge   Remove all config, data, and logs"
            echo "            Without --purge, config and data are retained for re-enrollment."
            exit 0
            ;;
        *) echo "Unknown option: $arg" >&2; exit 1 ;;
    esac
done

if [[ $EUID -ne 0 ]]; then
    echo "ERROR: This script must be run as root (use sudo)" >&2
    exit 1
fi

# Prefer using the agent binary's built-in uninstall if available.
if [[ -x "$BINARY" ]]; then
    if [[ "$PURGE" == true ]]; then
        exec "$BINARY" uninstall --purge
    else
        exec "$BINARY" uninstall
    fi
fi

# Fallback: manual uninstall if binary is missing.
echo "Agent binary not found, performing manual uninstall..."

SERVICE_NAME="moebius-agent"

# Stop and disable service.
systemctl stop "$SERVICE_NAME" 2>/dev/null || true
systemctl disable "$SERVICE_NAME" 2>/dev/null || true
rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
systemctl daemon-reload 2>/dev/null || true

# Remove binaries.
rm -f /usr/local/bin/moebius-agent
rm -f /usr/local/bin/moebius-agent.previous
rm -f /usr/local/bin/moebius-agent.new
rm -f /usr/local/bin/moebius-pkg-helper

# Remove socket and runtime dir.
rm -f /run/moebius-agent/moebius-agent.sock
rmdir /run/moebius-agent 2>/dev/null || true

# Remove user and group.
userdel moebius-agent 2>/dev/null || true
groupdel moebius-agent 2>/dev/null || true

if [[ "$PURGE" == true ]]; then
    echo "Purging configuration, data, and logs..."
    rm -rf /etc/moebius-agent
    rm -rf /var/lib/moebius-agent
    rm -rf /var/log/moebius-agent
else
    echo "Retaining configuration and data:"
    echo "  Config: /etc/moebius-agent"
    echo "  Data:   /var/lib/moebius-agent"
    echo "  Logs:   /var/log/moebius-agent"
fi

echo "Uninstall complete."
