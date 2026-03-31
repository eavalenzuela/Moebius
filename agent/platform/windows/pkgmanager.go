//go:build windows

package windows

import (
	"bytes"
	"os/exec"
	"strings"

	"github.com/eavalenzuela/Moebius/agent/platform"
)

// PkgManager implements platform.PackageManager for Windows using winget or Chocolatey.
type PkgManager struct {
	manager string // "winget" or "choco"
}

// NewPkgManager detects the available package manager and returns a PkgManager.
// If managerHint is non-empty, it overrides auto-detection.
func NewPkgManager(managerHint string) *PkgManager {
	mgr := managerHint
	if mgr == "" {
		mgr = detectManager()
	}
	return &PkgManager{manager: mgr}
}

// DetectedManager returns the detected package manager name.
func (m *PkgManager) DetectedManager() string { return m.manager }

// Install installs a package via the detected manager.
func (m *PkgManager) Install(name, version string) platform.PackageResult {
	if m.manager == "" {
		return platform.PackageResult{
			ExitCode: -1,
			Error:    "no supported package manager detected (need winget or choco)",
		}
	}
	args := buildInstallArgs(m.manager, name, version)
	return runCommand(m.manager, args)
}

// Remove removes a package via the detected manager.
func (m *PkgManager) Remove(name string) platform.PackageResult {
	if m.manager == "" {
		return platform.PackageResult{
			ExitCode: -1,
			Error:    "no supported package manager detected (need winget or choco)",
		}
	}
	args := buildRemoveArgs(m.manager, name)
	return runCommand(m.manager, args)
}

// Update updates a package via the detected manager.
func (m *PkgManager) Update(name, version string) platform.PackageResult {
	if m.manager == "" {
		return platform.PackageResult{
			ExitCode: -1,
			Error:    "no supported package manager detected (need winget or choco)",
		}
	}
	args := buildUpdateArgs(m.manager, name, version)
	return runCommand(m.manager, args)
}

func runCommand(program string, args []string) platform.PackageResult {
	cmd := exec.Command(program, args...) //nolint:gosec // controlled package manager invocation

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := platform.PackageResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		result.Error = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		return result
	}

	result.Success = true
	return result
}

func buildInstallArgs(manager, name, version string) []string {
	switch manager {
	case "winget":
		args := []string{"install", "--id", name, "--silent", "--accept-package-agreements", "--accept-source-agreements"}
		if version != "" {
			args = append(args, "--version", version)
		}
		return args
	case "choco":
		args := []string{"install", name, "-y", "--no-progress"}
		if version != "" {
			args = append(args, "--version", version)
		}
		return args
	default:
		return nil
	}
}

func buildRemoveArgs(manager, name string) []string {
	switch manager {
	case "winget":
		return []string{"uninstall", "--id", name, "--silent"}
	case "choco":
		return []string{"uninstall", name, "-y"}
	default:
		return nil
	}
}

func buildUpdateArgs(manager, name, version string) []string {
	switch manager {
	case "winget":
		args := []string{"upgrade", "--id", name, "--silent", "--accept-package-agreements", "--accept-source-agreements"}
		if version != "" {
			args = append(args, "--version", version)
		}
		return args
	case "choco":
		args := []string{"upgrade", name, "-y", "--no-progress"}
		if version != "" {
			args = append(args, "--version", version)
		}
		return args
	default:
		return nil
	}
}

func detectManager() string {
	// Prefer winget (built into modern Windows)
	if path, err := exec.LookPath("winget"); err == nil && path != "" {
		return "winget"
	}
	// Fall back to Chocolatey
	if path, err := exec.LookPath("choco"); err == nil && path != "" {
		return "choco"
	}
	return ""
}

// ValidatePackageName checks a package name for dangerous characters.
func ValidatePackageName(name string) bool {
	if name == "" {
		return false
	}
	return !strings.ContainsAny(name, ";|&$`\\\"'<>(){}!\n\r\t ")
}
