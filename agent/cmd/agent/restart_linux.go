//go:build linux

package main

import (
	"fmt"
	"os/exec"
)

func restartService(name string) error {
	cmd := exec.Command("systemctl", "restart", name) //nolint:gosec // controlled service name
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart %s: %s: %w", name, string(out), err)
	}
	return nil
}
