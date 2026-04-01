//go:build linux

package installer

import (
	"fmt"
	"os"
	"os/user"

	"github.com/eavalenzuela/Moebius/agent/platform"
)

// Uninstall removes the agent from the system.
// If purge is true, all configuration, data, and logs are also removed.
func Uninstall(plat platform.Platform, purge bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall must be run as root")
	}

	serviceName := plat.ServiceName()

	// Step 1: Stop and disable the systemd service.
	fmt.Println("Stopping and disabling service...")
	_ = runCmd("systemctl", "stop", serviceName)
	_ = runCmd("systemctl", "disable", serviceName)

	// Step 2: Remove systemd unit file and reload.
	unitFile := "/etc/systemd/system/" + serviceName + ".service"
	if err := removeIfExists(unitFile); err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove unit file: %v\n", err)
	}
	_ = runCmd("systemctl", "daemon-reload")
	fmt.Println("Service removed.")

	// Step 3: Remove agent binaries and setuid helper.
	fmt.Println("Removing binaries...")
	removeIfExists(plat.BinaryPath())                   //nolint:errcheck
	removeIfExists(plat.BinaryPreviousPath())           //nolint:errcheck
	removeIfExists(plat.BinaryStagingPath())            //nolint:errcheck
	removeIfExists("/usr/local/bin/moebius-pkg-helper") //nolint:errcheck

	// Step 4: Remove Unix socket.
	removeIfExists(plat.SocketPath()) //nolint:errcheck

	// Step 5: Remove runtime directory.
	if dir := plat.RuntimeDir(); dir != "" {
		removeIfExists(dir) //nolint:errcheck
	}

	// Step 6: Remove system user and group.
	fmt.Println("Removing service user and group...")
	if _, err := user.Lookup(serviceName); err == nil {
		if err := runCmd("userdel", serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove user: %v\n", err)
		}
	}
	if _, err := user.LookupGroup(serviceName); err == nil {
		if err := runCmd("groupdel", serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove group: %v\n", err)
		}
	}

	// Step 7 (purge only): Remove config, data, and log directories.
	if purge {
		fmt.Println("Purging configuration, data, and logs...")
		for _, dir := range []string{
			plat.ConfigDir(),
			plat.DataDir(),
			plat.LogDir(),
		} {
			if dir == "" {
				continue
			}
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", dir, err)
			}
		}
	} else {
		fmt.Println("Retaining configuration and data directories:")
		fmt.Printf("  Config: %s\n", plat.ConfigDir())
		fmt.Printf("  Data:   %s\n", plat.DataDir())
		if dir := plat.LogDir(); dir != "" {
			fmt.Printf("  Logs:   %s\n", dir)
		}
	}

	fmt.Println("Uninstall complete.")
	return nil
}
