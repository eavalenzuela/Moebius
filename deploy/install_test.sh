#!/usr/bin/env bash
# Smoke tests for install.sh — validates argument parsing and error paths.
# These tests do NOT require root and do NOT actually install anything.
# They verify that the script exits with expected errors for bad inputs.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_SCRIPT="${SCRIPT_DIR}/install.sh"
PASS=0
FAIL=0

assert_fails() {
    local desc="$1"
    shift
    if output=$(bash "$INSTALL_SCRIPT" "$@" 2>&1); then
        echo "FAIL: $desc (expected failure, got success)"
        echo "  output: $output"
        FAIL=$((FAIL + 1))
    else
        echo "PASS: $desc"
        PASS=$((PASS + 1))
    fi
}

assert_output_contains() {
    local desc="$1"
    local needle="$2"
    shift 2
    output=$(bash "$INSTALL_SCRIPT" "$@" 2>&1) || true
    if echo "$output" | grep -q "$needle"; then
        echo "PASS: $desc"
        PASS=$((PASS + 1))
    else
        echo "FAIL: $desc (expected output to contain '$needle')"
        echo "  output: $output"
        FAIL=$((FAIL + 1))
    fi
}

# --- Tests ---

# Test: Must be root.
if [[ $EUID -ne 0 ]]; then
    assert_fails "rejects non-root" --enrollment-token "test" --server-url "https://example.com" --binary /bin/true
    assert_output_contains "non-root error message" "must be run as root" --enrollment-token "test" --server-url "https://example.com" --binary /bin/true
fi

# Test: --help exits cleanly.
if bash "$INSTALL_SCRIPT" --help >/dev/null 2>&1; then
    echo "PASS: --help exits 0"
    PASS=$((PASS + 1))
else
    echo "FAIL: --help should exit 0"
    FAIL=$((FAIL + 1))
fi

# Test: --help shows usage text.
assert_output_contains "--help shows usage" "Usage:" --help

# Test: Unknown flag.
assert_fails "rejects unknown flag" --bogus-flag

# --- Summary ---
echo ""
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
    exit 1
fi
