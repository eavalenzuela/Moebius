//go:build linux

// pkg-helper is a minimal setuid helper binary for package management.
// It is installed setuid root and called by the unprivileged agent process
// to invoke apt-get or dnf. Only whitelisted operations are permitted.
//
// Usage: moebius-pkg-helper <manager> <action> <package> [version]
//
//	manager: "apt" or "dnf"
//	action:  "install", "remove", or "update"
//	package: package name (validated against shell metacharacters)
//	version: optional version constraint
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	linux "github.com/eavalenzuela/Moebius/agent/platform/linux"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "pkg-helper: %s\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Validate arguments strictly
	if err := linux.ValidateHelperArgs(args); err != nil {
		return err
	}

	manager := args[0]
	action := args[1]
	name := args[2]
	version := ""
	if len(args) == 4 {
		version = args[3]
	}

	// Build the native command
	binary, cmdArgs, err := linux.BuildNativeArgs(manager, action, name, version)
	if err != nil {
		return err
	}

	// Resolve binary path
	binPath, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("package manager not found: %s: %w", binary, err)
	}

	// Set clean environment
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"DEBIAN_FRONTEND=noninteractive",
		"LC_ALL=C",
	}

	// Exec into the package manager (replaces this process)
	return syscall.Exec(binPath, append([]string{binary}, cmdArgs...), env)
}
