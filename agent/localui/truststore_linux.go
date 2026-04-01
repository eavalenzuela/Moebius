//go:build linux

package localui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const certFileName = "moebius-agent-local-ca.crt"

// InstallCATrustStore copies the CA certificate into the system trust store
// and updates the trust database. Supports Debian/Ubuntu and RHEL/Fedora.
func InstallCATrustStore(caCertPath string) error {
	caPEM, err := os.ReadFile(caCertPath) //nolint:gosec // agent-controlled path
	if err != nil {
		return fmt.Errorf("read CA cert: %w", err)
	}

	// Debian/Ubuntu
	debianDir := "/usr/local/share/ca-certificates"
	if isDir(debianDir) {
		dest := filepath.Join(debianDir, certFileName)
		if err := os.WriteFile(dest, caPEM, 0o644); err != nil { //nolint:gosec // system CA store
			return fmt.Errorf("write CA to debian trust store: %w", err)
		}
		if out, err := exec.Command("update-ca-certificates").CombinedOutput(); err != nil {
			return fmt.Errorf("update-ca-certificates: %s: %w", string(out), err)
		}
		return nil
	}

	// RHEL/Fedora
	rhelDir := "/etc/pki/ca-trust/source/anchors"
	if isDir(rhelDir) {
		dest := filepath.Join(rhelDir, certFileName)
		if err := os.WriteFile(dest, caPEM, 0o644); err != nil { //nolint:gosec // system CA store
			return fmt.Errorf("write CA to rhel trust store: %w", err)
		}
		if out, err := exec.Command("update-ca-trust").CombinedOutput(); err != nil {
			return fmt.Errorf("update-ca-trust: %s: %w", string(out), err)
		}
		return nil
	}

	return fmt.Errorf("unsupported Linux distribution: no known CA trust store directory found")
}

// RemoveCATrustStore removes the CA certificate from the system trust store.
func RemoveCATrustStore() error {
	debianPath := filepath.Join("/usr/local/share/ca-certificates", certFileName)
	rhelPath := filepath.Join("/etc/pki/ca-trust/source/anchors", certFileName)

	removed := false
	if err := os.Remove(debianPath); err == nil {
		removed = true
		_, _ = exec.Command("update-ca-certificates", "--fresh").CombinedOutput()
	}
	if err := os.Remove(rhelPath); err == nil {
		removed = true
		_, _ = exec.Command("update-ca-trust").CombinedOutput()
	}

	if !removed {
		return fmt.Errorf("CA certificate not found in any trust store")
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
