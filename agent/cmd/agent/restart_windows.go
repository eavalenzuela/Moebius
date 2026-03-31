//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

func restartService(name string) error {
	// Stop then start via sc.exe
	if out, err := exec.Command("sc", "stop", name).CombinedOutput(); err != nil { //nolint:gosec // controlled service name
		return fmt.Errorf("sc stop %s: %s: %w", name, string(out), err)
	}
	if out, err := exec.Command("sc", "start", name).CombinedOutput(); err != nil { //nolint:gosec // controlled service name
		return fmt.Errorf("sc start %s: %s: %w", name, string(out), err)
	}
	return nil
}
