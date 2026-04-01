//go:build windows

package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eavalenzuela/Moebius/agent/platform"
)

// Install performs a manual (non-MSI) agent installation on Windows.
// This is the alternative path for operators who prefer not to use the MSI.
func Install(plat platform.Platform, enrollmentToken, serverURL, caCertPath string, cdmEnabled bool) error {
	// Must be running elevated (Administrator).
	if !isElevated() {
		return fmt.Errorf("install must be run as Administrator")
	}

	binaryDir := plat.BinaryDir()
	dataDir := plat.DataDir()
	dropDir := plat.DropDir()
	serviceName := plat.ServiceName()
	binaryPath := plat.BinaryPath()

	// Step 1: Create directory structure.
	fmt.Println("Creating directory structure...")
	for _, dir := range []string{binaryDir, dataDir, dropDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Step 2: Copy binary to install location.
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	selfPath, err = filepath.EvalSymlinks(selfPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	if selfPath != binaryPath {
		fmt.Printf("Installing binary to %s\n", binaryPath)
		if err := copyFile(selfPath, binaryPath); err != nil {
			return fmt.Errorf("copy binary: %w", err)
		}
	}

	// Step 3: Write configuration file.
	configPath := plat.ConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Println("Writing configuration...")
		cdmStr := "false"
		if cdmEnabled {
			cdmStr = "true"
		}
		dropDirEscaped := strings.ReplaceAll(dropDir, `\`, `\\`)
		config := fmt.Sprintf(`[server]
url = "%s"
poll_interval_seconds = 30

[storage]
drop_directory = "%s"
space_check_enabled = true
space_check_threshold = 0.50

[local_ui]
enabled = true
port = 57000

[logging]
level = "info"

[cdm]
enabled = %s
`, serverURL, dropDirEscaped, cdmStr)

		if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	} else {
		fmt.Println("Configuration already exists, skipping (upgrade).")
	}

	// Step 4: Write enrollment token.
	if enrollmentToken != "" {
		tokenPath := plat.EnrollmentTokenPath()
		fmt.Println("Writing enrollment token...")
		if err := os.WriteFile(tokenPath, []byte(enrollmentToken), 0o600); err != nil {
			return fmt.Errorf("write enrollment token: %w", err)
		}
	}

	// Step 5: Install CA certificate (if provided).
	if caCertPath != "" {
		caDestPath := plat.CACertPath()
		fmt.Println("Installing CA certificate...")
		if err := copyFile(caCertPath, caDestPath); err != nil {
			return fmt.Errorf("copy CA cert: %w", err)
		}
		// Import into Local Machine\Root store.
		if out, err := exec.Command("certutil.exe", "-addstore", "Root", caDestPath).CombinedOutput(); err != nil { //nolint:gosec // controlled input
			fmt.Fprintf(os.Stderr, "warning: certutil: %s: %v\n", string(out), err)
		}
	}

	// Step 6: Set ACLs on data directory.
	fmt.Println("Setting directory permissions...")
	setACLs(dataDir, serviceName)

	// Step 7: Register Windows Service.
	fmt.Println("Registering Windows Service...")
	if err := registerService(serviceName, binaryPath); err != nil {
		return fmt.Errorf("register service: %w", err)
	}

	// Step 8: Configure service recovery (restart on failure with 10s delay).
	fmt.Println("Configuring service recovery...")
	_ = runCmd("sc.exe", "failure", serviceName, "reset=", "86400",
		"actions=", "restart/10000/restart/10000/restart/10000")

	// Step 9: Start the service.
	fmt.Println("Starting service...")
	if err := runCmd("sc.exe", "start", serviceName); err != nil {
		return fmt.Errorf("start service: %w", err)
	}

	// Step 10: Wait for first check-in (up to 30s).
	fmt.Println("Waiting for agent to start (up to 30s)...")
	if waitForService(serviceName, 30) {
		agentIDPath := filepath.Join(dataDir, "agent_id")
		if id, err := os.ReadFile(agentIDPath); err == nil {
			fmt.Printf("Installation successful! Agent ID: %s\n", strings.TrimSpace(string(id)))
		} else {
			fmt.Println("Service is running. Enrollment may still be in progress.")
		}
	} else {
		fmt.Fprintln(os.Stderr, "Warning: service did not start within 30 seconds.")
		fmt.Fprintln(os.Stderr, "Check logs: Get-EventLog -LogName Application -Source MoebiusAgent -Newest 50")
	}

	fmt.Println("Install complete.")
	return nil
}

// registerService creates the Windows Service via sc.exe.
func registerService(name, binaryPath string) error {
	// Check if service already exists; if so, update it.
	checkOut, _ := exec.Command("sc.exe", "query", name).CombinedOutput() //nolint:gosec // controlled input
	if strings.Contains(string(checkOut), name) {
		// Service exists — stop it, delete, and re-create.
		_ = runCmd("sc.exe", "stop", name)
		_ = runCmd("sc.exe", "delete", name)
	}

	binPathArg := fmt.Sprintf(`"%s" run`, binaryPath)
	return runCmd("sc.exe", "create", name,
		"binPath=", binPathArg,
		"start=", "auto",
		"DisplayName=", "Moebius Device Management Agent",
		"obj=", fmt.Sprintf("NT SERVICE\\%s", name),
	)
}

// waitForService polls sc.exe query until the service is running or timeout.
func waitForService(name string, timeoutSec int) bool {
	for i := 0; i < timeoutSec; i++ {
		out, err := exec.Command("sc.exe", "query", name).CombinedOutput() //nolint:gosec // controlled input
		if err == nil && strings.Contains(string(out), "RUNNING") {
			return true
		}
		// Sleep 1 second via ping (avoid importing time for a build-tagged file).
		_ = exec.Command("ping", "-n", "2", "127.0.0.1").Run() //nolint:gosec
	}
	return false
}

// setACLs restricts the data directory to SYSTEM, Administrators, and the service account.
func setACLs(dataDir, serviceName string) {
	serviceAccount := fmt.Sprintf("NT SERVICE\\%s", serviceName)
	_ = exec.Command("icacls", dataDir, //nolint:gosec // controlled input
		"/inheritance:r",
		"/grant:r", "SYSTEM:(OI)(CI)F",
		"/grant:r", "BUILTIN\\Administrators:(OI)(CI)F",
		"/grant:r", fmt.Sprintf("%s:(OI)(CI)M", serviceAccount),
	).Run()
}

// isElevated checks whether the current process is running with Administrator privileges.
func isElevated() bool {
	// Attempt to open the ADMIN$ share — only succeeds if elevated.
	_, err := os.Open(`\\.\PHYSICALDRIVE0`)
	if err != nil {
		return false
	}
	return true
}
