//go:build linux

package installer

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/eavalenzuela/Moebius/agent/platform"
)

const systemdUnit = `[Unit]
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
`

// Install performs agent installation on Linux.
// It mirrors the logic in deploy/install.sh: creates the service user,
// directory structure, config, systemd unit, and starts the service.
func Install(plat platform.Platform, enrollmentToken, serverURL, caCertPath string, cdmEnabled bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install must be run as root (use sudo)")
	}

	// Require systemd.
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemd is required but systemctl was not found in PATH")
	}

	serviceName := plat.ServiceName()
	serviceUser := serviceName
	serviceGroup := serviceName
	binaryPath := plat.BinaryPath()
	configDir := plat.ConfigDir()
	dataDir := plat.DataDir()
	logDir := plat.LogDir()
	runtimeDir := plat.RuntimeDir()
	dropDir := plat.DropDir()

	// Detect existing installation.
	_, existingErr := os.Stat(binaryPath)
	upgrade := existingErr == nil
	if upgrade {
		fmt.Println("Existing installation detected — upgrading in place.")
	}

	// For new installs, enrollment token and server URL are required.
	if !upgrade {
		if enrollmentToken == "" {
			return fmt.Errorf("--enrollment-token is required for new installations")
		}
		if serverURL == "" {
			return fmt.Errorf("--server-url is required for new installations")
		}
	}

	// Step 1: Create system group and user.
	if _, err := user.LookupGroup(serviceGroup); err != nil {
		fmt.Printf("Creating system group: %s\n", serviceGroup)
		if err := runCmd("groupadd", "--system", serviceGroup); err != nil {
			return fmt.Errorf("create group: %w", err)
		}
	}
	if _, err := user.Lookup(serviceUser); err != nil {
		fmt.Printf("Creating system user: %s\n", serviceUser)
		if err := runCmd("useradd", "--system",
			"--gid", serviceGroup,
			"--home-dir", dataDir,
			"--no-create-home",
			"--shell", "/usr/sbin/nologin",
			serviceUser,
		); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
	}

	// Step 2: Create directory structure with correct ownership.
	fmt.Println("Creating directory structure...")
	dirs := []struct {
		path  string
		owner string
		group string
		mode  os.FileMode
	}{
		{configDir, "root", serviceGroup, 0o750},
		{dataDir, serviceUser, serviceGroup, 0o750},
		{dropDir, serviceUser, serviceGroup, 0o750},
		{logDir, serviceUser, serviceGroup, 0o750},
		{runtimeDir, serviceUser, serviceGroup, 0o750},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("create %s: %w", d.path, err)
		}
		_ = runCmd("chown", fmt.Sprintf("%s:%s", d.owner, d.group), d.path)
		if err := os.Chmod(d.path, d.mode); err != nil {
			return fmt.Errorf("chmod %s: %w", d.path, err)
		}
	}

	// Step 3: Install binary.
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	selfPath, err = filepath.EvalSymlinks(selfPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	if upgrade {
		fmt.Println("Backing up existing binary...")
		_ = copyFile(binaryPath, plat.BinaryPreviousPath())
		_ = runCmd("chown", "root:root", plat.BinaryPreviousPath())
		_ = os.Chmod(plat.BinaryPreviousPath(), 0o755) //nolint:gosec // binary needs exec permission
	}

	if selfPath != binaryPath {
		fmt.Printf("Installing binary to %s\n", binaryPath)
		if err := copyFile(selfPath, binaryPath); err != nil {
			return fmt.Errorf("copy binary: %w", err)
		}
		_ = runCmd("chown", "root:root", binaryPath)
		if err := os.Chmod(binaryPath, 0o755); err != nil { //nolint:gosec // binary needs exec permission
			return fmt.Errorf("chmod binary: %w", err)
		}
	}

	// Print version.
	if out, err := exec.Command(binaryPath, "version").CombinedOutput(); err == nil { //nolint:gosec // controlled path
		fmt.Printf("Installed: %s\n", strings.TrimSpace(string(out)))
	}

	// Step 3b: Install setuid package helper (if present alongside the agent binary).
	helperSrc := filepath.Join(filepath.Dir(selfPath), "moebius-pkg-helper")
	helperDst := "/usr/local/bin/moebius-pkg-helper"
	if _, statErr := os.Stat(helperSrc); statErr == nil {
		fmt.Printf("Installing setuid package helper to %s\n", helperDst)
		if err := copyFile(helperSrc, helperDst); err != nil {
			fmt.Fprintf(os.Stderr, "warning: copy pkg-helper: %v\n", err)
		} else {
			_ = runCmd("chown", "root:root", helperDst)
			if err := os.Chmod(helperDst, 0o4755); err != nil { //nolint:gosec // setuid helper needs 4755
				fmt.Fprintf(os.Stderr, "warning: chmod pkg-helper: %v\n", err)
			}
		}
	} else {
		fmt.Println("Package helper binary not found next to agent (skipping setuid helper).")
	}

	// Step 4: Write configuration (new install or missing config).
	configPath := plat.ConfigPath()
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) && serverURL != "" {
		fmt.Printf("Writing configuration to %s\n", configPath)
		cdmStr := "false"
		if cdmEnabled {
			cdmStr = "true"
		}
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
file = "%s/moebius-agent.log"

[cdm]
enabled = %s
`, serverURL, dropDir, logDir, cdmStr)

		if err := os.WriteFile(configPath, []byte(config), 0o640); err != nil { //nolint:gosec // config readable by service group
			return fmt.Errorf("write config: %w", err)
		}
		_ = runCmd("chown", fmt.Sprintf("root:%s", serviceGroup), configPath)
	} else if statErr == nil {
		fmt.Println("Configuration already exists, skipping (upgrade).")
	}

	// Step 5: Write enrollment token.
	if enrollmentToken != "" {
		tokenPath := plat.EnrollmentTokenPath()
		fmt.Println("Writing enrollment token...")
		if err := os.WriteFile(tokenPath, []byte(enrollmentToken), 0o600); err != nil {
			return fmt.Errorf("write enrollment token: %w", err)
		}
		_ = runCmd("chown", fmt.Sprintf("root:%s", serviceGroup), tokenPath)
	}

	// Step 6: Install CA certificate.
	if caCertPath != "" {
		caDestPath := plat.CACertPath()
		fmt.Println("Installing CA certificate...")
		if err := copyFile(caCertPath, caDestPath); err != nil {
			return fmt.Errorf("copy CA cert: %w", err)
		}
		_ = runCmd("chown", fmt.Sprintf("root:%s", serviceGroup), caDestPath)
		_ = os.Chmod(caDestPath, 0o644) //nolint:gosec // CA cert must be world-readable
	}

	// Step 7: Install systemd unit file.
	unitPath := "/etc/systemd/system/" + serviceName + ".service"
	fmt.Printf("Installing systemd unit file to %s\n", unitPath)
	if err := os.WriteFile(unitPath, []byte(systemdUnit), 0o644); err != nil { //nolint:gosec // systemd unit must be world-readable
		return fmt.Errorf("write unit file: %w", err)
	}

	// Step 8: Reload systemd and start service.
	fmt.Println("Reloading systemd daemon...")
	if err := runCmd("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	if upgrade {
		fmt.Println("Restarting agent service...")
		// Only restart if already running; otherwise enable + start.
		if err := exec.Command("systemctl", "is-active", "--quiet", serviceName).Run(); err == nil { //nolint:gosec // controlled path
			if err := runCmd("systemctl", "restart", serviceName); err != nil {
				return fmt.Errorf("restart service: %w", err)
			}
		} else {
			if err := runCmd("systemctl", "enable", "--now", serviceName); err != nil {
				return fmt.Errorf("enable service: %w", err)
			}
		}
	} else {
		fmt.Println("Enabling and starting agent service...")
		if err := runCmd("systemctl", "enable", "--now", serviceName); err != nil {
			return fmt.Errorf("enable service: %w", err)
		}
	}

	// Step 9: Wait for service to start and first check-in.
	fmt.Println("Waiting for agent to start (up to 30s)...")
	started := false
	for i := 0; i < 30; i++ {
		if exec.Command("systemctl", "is-active", "--quiet", serviceName).Run() == nil { //nolint:gosec // controlled path
			started = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !started {
		fmt.Fprintln(os.Stderr, "Warning: agent service did not start within 30 seconds.")
		fmt.Fprintf(os.Stderr, "Check logs: journalctl -u %s --no-pager -n 50\n", serviceName)
		return nil
	}

	// Wait for enrollment (agent_id file appears).
	fmt.Println("Waiting for enrollment (up to 30s)...")
	agentIDPath := plat.AgentIDPath()
	for i := 0; i < 30; i++ {
		if _, err := os.Stat(agentIDPath); err == nil {
			id, _ := os.ReadFile(agentIDPath) //nolint:gosec // known path
			fmt.Printf("Installation successful! Agent ID: %s\n", strings.TrimSpace(string(id)))
			fmt.Printf("Service:  systemctl status %s\n", serviceName)
			fmt.Printf("Logs:     journalctl -u %s -f\n", serviceName)
			return nil
		}
		// Check service didn't crash.
		if exec.Command("systemctl", "is-active", "--quiet", serviceName).Run() != nil { //nolint:gosec // controlled path
			fmt.Fprintln(os.Stderr, "Agent service stopped unexpectedly.")
			fmt.Fprintf(os.Stderr, "Check logs: journalctl -u %s --no-pager -n 50\n", serviceName)
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	if upgrade {
		// Upgrades may already be enrolled.
		if _, err := os.Stat(agentIDPath); err == nil {
			fmt.Println("Upgrade successful! Agent is running.")
		} else {
			fmt.Println("Agent is running but enrollment has not completed within 30s.")
			fmt.Printf("Check status: systemctl status %s\n", serviceName)
		}
	} else {
		fmt.Println("Agent is running but enrollment has not completed within 30s.")
		fmt.Println("This may be normal if the server is unreachable.")
		fmt.Printf("Check status: systemctl status %s\n", serviceName)
	}

	return nil
}
