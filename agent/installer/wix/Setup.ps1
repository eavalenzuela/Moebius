# Moebius Agent — MSI Post-Install Setup Script
# Called as a deferred custom action during MSI installation.
#
# Parameters (passed as named args by the MSI custom action):
#   -Action       install | purge
#   -DataDir      C:\ProgramData\MoebiusAgent
#   -ServerUrl    https://manage.example.com
#   -Token        enrollment token value
#   -CdmEnabled   0 or 1

param(
    [Parameter(Mandatory)]
    [ValidateSet('install', 'purge')]
    [string]$Action,

    [Parameter(Mandatory)]
    [string]$DataDir,

    [string]$ServerUrl = '',
    [string]$Token = '',
    [string]$CdmEnabled = '0'
)

$ErrorActionPreference = 'Stop'

function Write-Log {
    param([string]$Message)
    $ts = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
    Write-Host "[$ts] $Message"
}

# ---------------------------------------------------------------------------
# Install action
# ---------------------------------------------------------------------------
if ($Action -eq 'install') {

    # 1. Write config.toml (only for new installs — skip if config already exists)
    $configPath = Join-Path $DataDir 'config.toml'
    if ($ServerUrl -and -not (Test-Path $configPath)) {
        Write-Log "Writing config.toml"
        $cdmBool = if ($CdmEnabled -eq '1') { 'true' } else { 'false' }
        $dropDir = (Join-Path $DataDir 'drop') -replace '\\', '\\\\'

        $config = @"
[server]
url = "$ServerUrl"
poll_interval_seconds = 30

[storage]
drop_directory = "$dropDir"
space_check_enabled = true
space_check_threshold = 0.50

[local_ui]
enabled = true
port = 57000

[logging]
level = "info"

[cdm]
enabled = $cdmBool
"@
        Set-Content -Path $configPath -Value $config -Encoding UTF8
        Write-Log "config.toml written"
    }
    elseif (Test-Path $configPath) {
        Write-Log "config.toml already exists, skipping (upgrade)"
    }

    # 2. Write enrollment token
    if ($Token) {
        $tokenPath = Join-Path $DataDir 'enrollment.token'
        Write-Log "Writing enrollment token"
        Set-Content -Path $tokenPath -Value $Token -NoNewline -Encoding UTF8
    }

    # 3. Import CA certificate into Local Machine\Root store (if ca.crt exists)
    $caPath = Join-Path $DataDir 'ca.crt'
    if (Test-Path $caPath) {
        Write-Log "Importing CA certificate to Local Machine\Root store"
        $result = & certutil.exe -addstore Root $caPath 2>&1
        Write-Log "certutil: $result"
    }

    # 4. Set ACLs on ProgramData directory
    #    SYSTEM: Full Control (inherited)
    #    Administrators: Full Control (inherited)
    #    NT SERVICE\MoebiusAgent: Modify (inherited) — agent needs RW for config, certs, data
    Write-Log "Setting ACLs on $DataDir"
    & icacls $DataDir /inheritance:r `
        /grant:r 'SYSTEM:(OI)(CI)F' `
        /grant:r 'BUILTIN\Administrators:(OI)(CI)F' `
        /grant:r 'NT SERVICE\MoebiusAgent:(OI)(CI)M' `
        2>&1 | Out-Null

    # Tighter ACL on enrollment token and private key (SYSTEM-only read/write)
    foreach ($sensitive in @('enrollment.token', 'client.key')) {
        $path = Join-Path $DataDir $sensitive
        if (Test-Path $path) {
            & icacls $path /inheritance:r `
                /grant:r 'SYSTEM:(F)' `
                /grant:r 'BUILTIN\Administrators:(F)' `
                2>&1 | Out-Null
        }
    }

    # 5. Configure service recovery: restart on all failures with 10s delay
    Write-Log "Configuring service recovery actions"
    & sc.exe failure MoebiusAgent reset= 86400 actions= restart/10000/restart/10000/restart/10000 2>&1 | Out-Null

    Write-Log "Post-install setup complete"
}

# ---------------------------------------------------------------------------
# Purge action (on uninstall with PURGE=1)
# ---------------------------------------------------------------------------
if ($Action -eq 'purge') {
    Write-Log "Purging agent data"

    # Remove CA certificate from Local Machine\Root store
    Write-Log "Removing Moebius CA certificates from store"
    Get-ChildItem Cert:\LocalMachine\Root |
        Where-Object { $_.Subject -like '*Moebius*' } |
        Remove-Item -Force -ErrorAction SilentlyContinue

    # Remove ProgramData directory entirely
    if (Test-Path $DataDir) {
        Write-Log "Removing $DataDir"
        Remove-Item -Recurse -Force $DataDir -ErrorAction SilentlyContinue
    }

    Write-Log "Purge complete"
}
