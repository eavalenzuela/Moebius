//go:build windows

package installer

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/eavalenzuela/Moebius/agent/platform"
)

// Uninstall removes the agent from the system.
// If purge is true, all configuration, data (ProgramData), and the CA
// certificate in the Local Machine\Root store are also removed.
func Uninstall(plat platform.Platform, purge bool) error {
	if !isElevated() {
		return fmt.Errorf("uninstall must be run as Administrator")
	}

	serviceName := plat.ServiceName()

	// Step 1: Stop and remove the Windows Service.
	fmt.Println("Stopping service...")
	if serviceExists(serviceName) {
		_ = runCmd("sc.exe", "stop", serviceName)
		fmt.Println("Removing service...")
		if err := runCmd("sc.exe", "delete", serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove service: %v\n", err)
		}
	} else {
		fmt.Println("Service not found, skipping.")
	}
	fmt.Println("Service removed.")

	// Step 2: Remove agent binaries from Program Files.
	fmt.Println("Removing binaries...")
	removeIfExists(plat.BinaryPath())         //nolint:errcheck
	removeIfExists(plat.BinaryPreviousPath()) //nolint:errcheck
	removeIfExists(plat.BinaryStagingPath())  //nolint:errcheck

	// Remove Setup.ps1 if left behind from MSI install.
	setupScript := plat.BinaryDir() + `\Setup.ps1`
	removeIfExists(setupScript) //nolint:errcheck

	// Remove install directory if empty.
	removeEmptyDir(plat.BinaryDir())

	// Step 3: Remove named pipe (Windows cleans this up when the service
	// stops, but attempt removal for good measure).
	removeIfExists(plat.SocketPath()) //nolint:errcheck

	// Step 4 (purge only): Remove ProgramData and CA cert from store.
	if purge {
		fmt.Println("Purging configuration and data...")

		// Remove CA certificate from Local Machine\Root store.
		fmt.Println("Removing Moebius CA certificates from certificate store...")
		removeCACertFromStore()

		// Remove ProgramData\MoebiusAgent entirely.
		dataDir := plat.DataDir()
		if dataDir != "" {
			if err := os.RemoveAll(dataDir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", dataDir, err)
			}
		}
	} else {
		fmt.Println("Retaining configuration and data:")
		fmt.Printf("  Data: %s\n", plat.DataDir())
	}

	fmt.Println("Uninstall complete.")
	return nil
}

// serviceExists checks if a Windows Service with the given name is registered.
func serviceExists(name string) bool {
	out, err := exec.Command("sc.exe", "query", name).CombinedOutput() //nolint:gosec // controlled input
	if err != nil {
		return false
	}
	return strings.Contains(string(out), name)
}

// removeCACertFromStore removes any Moebius-related CA certificates
// from the Local Machine\Root certificate store.
func removeCACertFromStore() {
	// Use PowerShell to find and remove certs with "Moebius" in the subject.
	out, err := exec.Command("powershell.exe", //nolint:gosec // controlled command
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command",
		`Get-ChildItem Cert:\LocalMachine\Root | Where-Object { $_.Subject -like '*Moebius*' } | Remove-Item -Force -ErrorAction SilentlyContinue`,
	).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove CA cert: %s: %v\n", string(out), err)
	}
}

// removeEmptyDir removes a directory only if it is empty.
func removeEmptyDir(path string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		os.Remove(path) //nolint:errcheck
	}
}
