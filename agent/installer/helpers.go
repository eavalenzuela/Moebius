package installer

import (
	"os"
	"os/exec"
)

// runCmd executes a command, directing stdout/stderr to the console.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // controlled inputs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// copyFile copies src to dst atomically-ish (read all, write all).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// removeIfExists removes a file or directory if it exists.
func removeIfExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(path)
}
