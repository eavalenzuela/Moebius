# Moebius Agent — MSI Build Script
# Requires: WiX Toolset v4 (https://wixtoolset.org/)
#
# Usage:
#   .\build.ps1 -Version 1.0.0
#   .\build.ps1 -Version 1.0.0 -BinaryPath .\dist\agent-windows-amd64.exe
#
# Output: dist\agent-windows-amd64-<version>.msi

param(
    [Parameter(Mandatory)]
    [string]$Version,

    [string]$BinaryPath = "dist\agent-windows-amd64.exe",
    [string]$OutputDir = "dist"
)

$ErrorActionPreference = 'Stop'

# Resolve paths relative to repo root (script lives in agent/installer/wix/).
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..') | Select-Object -ExpandProperty Path
$wxsPath = Join-Path $PSScriptRoot 'Product.wxs'
$setupPath = Join-Path $PSScriptRoot 'Setup.ps1'
$binaryFullPath = Join-Path $repoRoot $BinaryPath
$outputDirFull = Join-Path $repoRoot $OutputDir
$outputMsi = Join-Path $outputDirFull "agent-windows-amd64-${Version}.msi"

# Validate inputs.
if (-not (Test-Path $binaryFullPath)) {
    Write-Error "Agent binary not found at: $binaryFullPath"
    exit 1
}

if (-not (Get-Command 'wix' -ErrorAction SilentlyContinue)) {
    Write-Error "WiX Toolset v4 CLI ('wix') not found in PATH. Install from https://wixtoolset.org/"
    exit 1
}

# Ensure output directory exists.
if (-not (Test-Path $outputDirFull)) {
    New-Item -ItemType Directory -Path $outputDirFull -Force | Out-Null
}

Write-Host "Building MSI installer..."
Write-Host "  Version:  $Version"
Write-Host "  Binary:   $binaryFullPath"
Write-Host "  WXS:      $wxsPath"
Write-Host "  Output:   $outputMsi"

wix build `
    -d "ProductVersion=$Version" `
    -d "AgentBinaryPath=$binaryFullPath" `
    -d "SetupScriptPath=$setupPath" `
    -arch x64 `
    -o $outputMsi `
    $wxsPath

if ($LASTEXITCODE -ne 0) {
    Write-Error "WiX build failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

$msiSize = (Get-Item $outputMsi).Length
$msiSizeMB = [math]::Round($msiSize / 1MB, 2)
Write-Host "MSI built successfully: $outputMsi ($msiSizeMB MB)"
