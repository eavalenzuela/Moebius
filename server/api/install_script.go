package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
)

// InstallScriptHandler serves dynamically generated install scripts
// and generates one-liner install commands for enrollment tokens.
type InstallScriptHandler struct {
	pool       *pgxpool.Pool
	enrollment *auth.EnrollmentService
	audit      *audit.Logger
	log        *slog.Logger
}

// NewInstallScriptHandler creates an InstallScriptHandler.
func NewInstallScriptHandler(pool *pgxpool.Pool, enrollment *auth.EnrollmentService, auditLog *audit.Logger, log *slog.Logger) *InstallScriptHandler {
	return &InstallScriptHandler{
		pool:       pool,
		enrollment: enrollment,
		audit:      auditLog,
		log:        log,
	}
}

// installerRow is the subset of the installers table needed to generate scripts.
type installerRow struct {
	Version  string
	SHA256   string
	FileID   string
	KeyID    string
	Yanked   bool
	Channel  string
	OS       string
	Arch     string
	Released time.Time
}

// ServeInstallScript handles GET /v1/install/{os}/{arch}?token=<enrollment_token>.
// It validates the token (without consuming it), looks up the latest stable
// installer for the platform, and returns a shell or PowerShell script that
// downloads, verifies, and runs the installer with the token embedded.
func (h *InstallScriptHandler) ServeInstallScript(w http.ResponseWriter, r *http.Request) {
	targetOS := chi.URLParam(r, "os")
	targetArch := chi.URLParam(r, "arch")
	rawToken := r.URL.Query().Get("token")

	// Validate OS/arch.
	if !isValidOS(targetOS) {
		Error(w, http.StatusBadRequest, "invalid os: must be linux or windows")
		return
	}
	if !isValidArch(targetOS, targetArch) {
		Error(w, http.StatusBadRequest, "invalid arch for os")
		return
	}

	// Validate enrollment token (peek — do not consume).
	if rawToken == "" {
		Error(w, http.StatusUnauthorized, "enrollment token required: ?token=<token>")
		return
	}
	_, err := h.enrollment.Peek(r.Context(), rawToken)
	if err != nil {
		h.log.Warn("install script: invalid token", slog.String("error", err.Error()))
		Error(w, http.StatusUnauthorized, "enrollment token invalid, already used, or expired")
		return
	}

	// Look up latest stable, non-yanked installer for this platform.
	inst, err := h.latestInstaller(r.Context(), targetOS, targetArch)
	if err != nil {
		h.log.Error("install script: no installer found",
			slog.String("os", targetOS), slog.String("arch", targetArch),
			slog.String("error", err.Error()))
		Error(w, http.StatusNotFound, "no installer available for this platform")
		return
	}

	// Derive server base URL from the request.
	baseURL := serverBaseURL(r)

	// Generate platform-appropriate install script.
	var script string
	var contentType string
	switch targetOS {
	case "linux":
		script = generateLinuxScript(baseURL, rawToken, inst)
		contentType = "text/x-shellscript; charset=utf-8"
	case "windows":
		script = generateWindowsScript(baseURL, rawToken, inst)
		contentType = "text/plain; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="install-%s-%s.%s"`, targetOS, targetArch, scriptExt(targetOS)))
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, script)
}

// installCommandRequest is the body for POST /v1/enrollment-tokens/{token_id}/install-command.
type installCommandRequest struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type installCommandResponse struct {
	Command   string    `json:"command"`
	ExpiresAt time.Time `json:"expires_at"`
	TokenID   string    `json:"token_id"`
}

// GenerateInstallCommand handles POST /v1/enrollment-tokens/{token_id}/install-command.
// It generates a one-liner install command for the given token and platform.
func (h *InstallScriptHandler) GenerateInstallCommand(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	tokenID := chi.URLParam(r, "token_id")

	var req installCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !isValidOS(req.OS) {
		Error(w, http.StatusBadRequest, "invalid os: must be linux or windows")
		return
	}
	if !isValidArch(req.OS, req.Arch) {
		Error(w, http.StatusBadRequest, "invalid arch for os")
		return
	}

	// Look up the token to get the raw token hash and expiry.
	// We only have the token ID here, not the raw token. We can't reconstruct
	// the raw token from the hash. Instead, return a command template that
	// tells the operator to substitute their token.
	var expiresAt time.Time
	err := h.pool.QueryRow(r.Context(),
		`SELECT expires_at FROM enrollment_tokens
		 WHERE id = $1 AND tenant_id = $2 AND used_at IS NULL`,
		tokenID, tenantID,
	).Scan(&expiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			Error(w, http.StatusNotFound, "enrollment token not found, already used, or expired")
			return
		}
		h.log.Error("install-command: query token", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to look up enrollment token")
		return
	}

	baseURL := serverBaseURL(r)

	var command string
	switch req.OS {
	case "linux":
		command = fmt.Sprintf(
			`curl -fsSL '%s/v1/install/linux/%s?token=<ENROLLMENT_TOKEN>' | sudo bash`,
			baseURL, req.Arch)
	case "windows":
		command = fmt.Sprintf(
			`powershell -NoProfile -ExecutionPolicy Bypass -Command "& { iwr -UseBasicParsing '%s/v1/install/windows/%s?token=<ENROLLMENT_TOKEN>' | iex }"`,
			baseURL, req.Arch)
	}

	JSON(w, http.StatusOK, installCommandResponse{
		Command:   command,
		ExpiresAt: expiresAt,
		TokenID:   tokenID,
	})
}

// latestInstaller returns the latest stable, non-yanked installer for the given os/arch.
func (h *InstallScriptHandler) latestInstaller(ctx context.Context, targetOS, targetArch string) (*installerRow, error) {
	var inst installerRow
	err := h.pool.QueryRow(ctx,
		`SELECT version, sha256, file_id, signature_key_id, yanked, channel, os, arch, released_at
		 FROM installers
		 WHERE os = $1 AND arch = $2 AND channel = 'stable' AND yanked = false
		 ORDER BY released_at DESC
		 LIMIT 1`,
		targetOS, targetArch,
	).Scan(
		&inst.Version, &inst.SHA256, &inst.FileID, &inst.KeyID,
		&inst.Yanked, &inst.Channel, &inst.OS, &inst.Arch, &inst.Released,
	)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

// serverBaseURL derives the public-facing server URL from the request.
func serverBaseURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		// Check X-Forwarded-Proto (common behind reverse proxy).
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		} else {
			scheme = "http"
		}
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	return strings.TrimRight(fmt.Sprintf("%s://%s", scheme, host), "/")
}

func isValidOS(os string) bool {
	return os == "linux" || os == "windows"
}

func isValidArch(os, arch string) bool {
	switch os {
	case "linux":
		return arch == "amd64" || arch == "arm64"
	case "windows":
		return arch == "amd64"
	}
	return false
}

func scriptExt(os string) string {
	if os == "windows" {
		return "ps1"
	}
	return "sh"
}

// ---------------------------------------------------------------------------
// Script generation
// ---------------------------------------------------------------------------

func generateLinuxScript(baseURL, token string, inst *installerRow) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
# Moebius Agent — Generated Install Script
# Platform: linux/%s  Version: %s
# This script was generated by the Moebius management server.
# The enrollment token is embedded below and will be consumed on first check-in.

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
fatal() { error "$@"; exit 1; }

# Must be root.
if [[ $EUID -ne 0 ]]; then
    fatal "This script must be run as root (use sudo)"
fi

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
cd "$WORK_DIR"

SERVER="%s"
VERSION="%s"
EXPECTED_SHA256="%s"
ENROLLMENT_TOKEN="%s"

info "Downloading installer (linux/%s v${VERSION})..."
curl -fsSL -o installer.tar.gz \
    "${SERVER}/v1/installers/linux/%s/${VERSION}" \
    -H "Authorization: Bearer ${ENROLLMENT_TOKEN}"

info "Downloading checksum..."
curl -fsSL -o installer.tar.gz.sha256 \
    "${SERVER}/v1/installers/linux/%s/${VERSION}/checksum" \
    -H "Authorization: Bearer ${ENROLLMENT_TOKEN}"

info "Downloading signature..."
curl -fsSL -o installer.tar.gz.sig \
    "${SERVER}/v1/installers/linux/%s/${VERSION}/signature" \
    -H "Authorization: Bearer ${ENROLLMENT_TOKEN}"

# Verify SHA-256 checksum.
info "Verifying checksum..."
ACTUAL_SHA256=$(sha256sum installer.tar.gz | awk '{print $1}')
if [[ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]]; then
    fatal "Checksum mismatch! Expected: ${EXPECTED_SHA256}  Got: ${ACTUAL_SHA256}"
fi
info "Checksum verified."

# Extract tarball.
info "Extracting installer..."
tar xzf installer.tar.gz

# Make binary executable.
chmod +x moebius-agent

# Run the agent install subcommand with the embedded enrollment token.
info "Running installer..."
./moebius-agent install \
    --enrollment-token "$ENROLLMENT_TOKEN" \
    --server-url "$SERVER"

info "Done."
`, inst.Arch, inst.Version,
		baseURL, inst.Version, inst.SHA256, token,
		inst.Arch, inst.Arch, inst.Arch, inst.Arch)
}

func generateWindowsScript(baseURL, token string, inst *installerRow) string {
	// Go raw string literals (delimited by backticks) cannot contain backticks.
	// We build the PowerShell script using double-quoted Go strings instead.
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	w("# Moebius Agent \u2014 Generated Install Script")
	w(fmt.Sprintf("# Platform: windows/%s  Version: %s", inst.Arch, inst.Version))
	w("# This script was generated by the Moebius management server.")
	w("# The enrollment token is embedded below and will be consumed on first check-in.")
	w("# Run as Administrator: powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1")
	w("")
	w("$ErrorActionPreference = 'Stop'")
	w("")
	w(fmt.Sprintf("$Server       = '%s'", baseURL))
	w(fmt.Sprintf("$Version      = '%s'", inst.Version))
	w(fmt.Sprintf("$ExpectedHash = '%s'", inst.SHA256))
	w(fmt.Sprintf("$Token        = '%s'", token))
	w("")
	w("function Write-Status($msg) { Write-Host \"[INFO]  $msg\" -ForegroundColor Green }")
	w("function Write-Err($msg)    { Write-Host \"[ERROR] $msg\" -ForegroundColor Red }")
	w("")
	w("# Must be Administrator.")
	w("$identity = [Security.Principal.WindowsIdentity]::GetCurrent()")
	w("$principal = New-Object Security.Principal.WindowsPrincipal($identity)")
	w("if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {")
	w("    Write-Err 'This script must be run as Administrator'")
	w("    exit 1")
	w("}")
	w("")
	w("$WorkDir = Join-Path $env:TEMP ('moebius-install-' + (Get-Random))")
	w("New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null")
	w("")
	w("try {")
	w("    $headers = @{ 'Authorization' = \"Bearer $Token\" }")
	w("")
	w(fmt.Sprintf("    Write-Status 'Downloading installer (windows/%s v%s)...'", inst.Arch, inst.Version))
	w(fmt.Sprintf("    $msiPath = Join-Path $WorkDir 'agent-windows-%s-%s.msi'", inst.Arch, inst.Version))
	w(fmt.Sprintf("    Invoke-WebRequest -UseBasicParsing -Uri \"$Server/v1/installers/windows/%s/$Version\" -Headers $headers -OutFile $msiPath", inst.Arch))
	w("")
	w("    Write-Status 'Downloading checksum...'")
	w("    $checksumPath = Join-Path $WorkDir 'checksum.sha256'")
	w(fmt.Sprintf("    Invoke-WebRequest -UseBasicParsing -Uri \"$Server/v1/installers/windows/%s/$Version/checksum\" -Headers $headers -OutFile $checksumPath", inst.Arch))
	w("")
	w("    Write-Status 'Downloading signature...'")
	w("    $sigPath = Join-Path $WorkDir 'installer.msi.sig'")
	w(fmt.Sprintf("    Invoke-WebRequest -UseBasicParsing -Uri \"$Server/v1/installers/windows/%s/$Version/signature\" -Headers $headers -OutFile $sigPath", inst.Arch))
	w("")
	w("    # Verify SHA-256 checksum.")
	w("    Write-Status 'Verifying checksum...'")
	w("    $actualHash = (Get-FileHash $msiPath -Algorithm SHA256).Hash.ToLower()")
	w("    if ($actualHash -ne $ExpectedHash) {")
	w("        Write-Err \"Checksum mismatch! Expected: $ExpectedHash  Got: $actualHash\"")
	w("        exit 1")
	w("    }")
	w("    Write-Status 'Checksum verified.'")
	w("")
	w("    # Run MSI installer silently with enrollment token and server URL.")
	w("    Write-Status 'Running MSI installer...'")
	w("    $msiArgs = \"/i `\"$msiPath`\" /quiet ENROLLMENT_TOKEN=$Token SERVER_URL=$Server\"")
	w("    $proc = Start-Process msiexec.exe -ArgumentList $msiArgs -Wait -PassThru")
	w("    if ($proc.ExitCode -ne 0) {")
	w("        Write-Err \"MSI installer failed with exit code $($proc.ExitCode)\"")
	w("        exit $proc.ExitCode")
	w("    }")
	w("")
	w("    Write-Status 'Installation complete.'")
	w("")
	w("    # Wait for service to start.")
	w("    Write-Status 'Waiting for agent service to start (up to 30s)...'")
	w("    for ($i = 0; $i -lt 30; $i++) {")
	w("        $svc = Get-Service -Name MoebiusAgent -ErrorAction SilentlyContinue")
	w("        if ($svc -and $svc.Status -eq 'Running') {")
	w("            Write-Status 'Agent service is running.'")
	w("            break")
	w("        }")
	w("        Start-Sleep -Seconds 1")
	w("    }")
	w("")
	w("    Write-Status 'Done.'")
	w("}")
	w("finally {")
	w("    Remove-Item -Recurse -Force $WorkDir -ErrorAction SilentlyContinue")
	w("}")

	return b.String()
}
