//go:build linux

package linux

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/eavalenzuela/Moebius/agent/platform"
)

const (
	// DefaultHelperPath is the expected location of the setuid helper binary.
	DefaultHelperPath = "/usr/local/bin/moebius-pkg-helper"
)

// PkgManager implements platform.PackageManager for Linux using apt-get or dnf,
// invoked through a minimal setuid helper binary for privilege escalation.
type PkgManager struct {
	manager    string // "apt" or "dnf"
	helperPath string // path to setuid helper binary
}

// NewPkgManager detects the available package manager and returns a PkgManager.
// If managerHint is non-empty, it overrides auto-detection.
// helperPath overrides the default setuid helper location if non-empty.
func NewPkgManager(managerHint, helperPath string) *PkgManager {
	if helperPath == "" {
		helperPath = DefaultHelperPath
	}
	mgr := managerHint
	if mgr == "" {
		mgr = detectManager()
	}
	return &PkgManager{manager: mgr, helperPath: helperPath}
}

// DetectedManager returns the detected package manager name.
func (m *PkgManager) DetectedManager() string { return m.manager }

// Install installs a package via the detected manager.
func (m *PkgManager) Install(name, version string) platform.PackageResult {
	return m.run("install", name, version)
}

// Remove removes a package via the detected manager.
func (m *PkgManager) Remove(name string) platform.PackageResult {
	return m.run("remove", name, "")
}

// Update updates a package via the detected manager.
func (m *PkgManager) Update(name, version string) platform.PackageResult {
	return m.run("update", name, version)
}

// run invokes the setuid helper with the given operation.
// The helper is called as: moebius-pkg-helper <manager> <action> <package> [version]
func (m *PkgManager) run(action, name, version string) platform.PackageResult {
	if m.manager == "" {
		return platform.PackageResult{
			ExitCode: -1,
			Error:    "no supported package manager detected (need apt-get or dnf)",
		}
	}

	args := buildHelperArgs(m.manager, action, name, version)
	cmd := exec.Command(m.helperPath, args...) //nolint:gosec // controlled args via setuid helper

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set non-interactive environment
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"DEBIAN_FRONTEND=noninteractive",
		"LC_ALL=C",
	}

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

// buildHelperArgs constructs the argument list for the setuid helper.
func buildHelperArgs(manager, action, name, version string) []string {
	args := []string{manager, action, name}
	if version != "" {
		args = append(args, version)
	}
	return args
}

// detectManager checks which package manager is available on this system.
func detectManager() string {
	// Prefer apt-get on Debian/Ubuntu systems
	if path, err := exec.LookPath("apt-get"); err == nil && path != "" {
		return "apt"
	}
	// Fall back to dnf on RHEL/Fedora
	if path, err := exec.LookPath("dnf"); err == nil && path != "" {
		return "dnf"
	}
	return ""
}

// BuildNativeArgs returns the native package manager command arguments
// for a given manager, action, package name, and version.
// This is used by the setuid helper to construct the actual command.
func BuildNativeArgs(manager, action, name, version string) (string, []string, error) {
	switch manager {
	case "apt":
		return buildAptArgs(action, name, version)
	case "dnf":
		return buildDnfArgs(action, name, version)
	default:
		return "", nil, fmt.Errorf("unsupported manager: %s", manager)
	}
}

func buildAptArgs(action, name, version string) (string, []string, error) {
	pkg := name
	if version != "" {
		pkg = name + "=" + version
	}

	switch action {
	case "install":
		return "apt-get", []string{"install", "-y", "--no-install-recommends", pkg}, nil
	case "remove":
		return "apt-get", []string{"remove", "-y", name}, nil
	case "update":
		// apt uses install for updates; update the package index first via the helper
		return "apt-get", []string{"install", "-y", "--only-upgrade", pkg}, nil
	default:
		return "", nil, fmt.Errorf("unsupported action: %s", action)
	}
}

func buildDnfArgs(action, name, version string) (string, []string, error) {
	pkg := name
	if version != "" {
		pkg = name + "-" + version
	}

	switch action {
	case "install":
		return "dnf", []string{"install", "-y", pkg}, nil
	case "remove":
		return "dnf", []string{"remove", "-y", name}, nil
	case "update":
		return "dnf", []string{"update", "-y", pkg}, nil
	default:
		return "", nil, fmt.Errorf("unsupported action: %s", action)
	}
}

// ValidateHelperArgs validates the arguments passed to the setuid helper.
// Returns an error if the arguments are invalid or potentially dangerous.
func ValidateHelperArgs(args []string) error {
	if len(args) < 3 || len(args) > 4 {
		return fmt.Errorf("expected 3-4 arguments: <manager> <action> <package> [version]")
	}

	manager := args[0]
	action := args[1]
	name := args[2]

	// Validate manager
	switch manager {
	case "apt", "dnf":
		// OK
	default:
		return fmt.Errorf("invalid manager: %q", manager)
	}

	// Validate action
	switch action {
	case "install", "remove", "update":
		// OK
	default:
		return fmt.Errorf("invalid action: %q", action)
	}

	// Validate package name — reject shell metacharacters
	if name == "" {
		return fmt.Errorf("empty package name")
	}
	if strings.ContainsAny(name, ";|&$`\\\"'<>(){}!\n\r\t ") {
		return fmt.Errorf("invalid package name: contains shell metacharacters")
	}

	// Validate version if present
	if len(args) == 4 {
		version := args[3]
		if strings.ContainsAny(version, ";|&$`\\\"'<>(){}!\n\r\t ") {
			return fmt.Errorf("invalid version: contains shell metacharacters")
		}
	}

	return nil
}
