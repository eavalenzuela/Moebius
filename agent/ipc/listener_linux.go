//go:build linux

package ipc

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// createListener creates a Unix domain socket at path with mode 0660 and
// group ownership set to "agent-users" (if the group exists).
func createListener(path string) (net.Listener, error) {
	// Remove stale socket from a previous run.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", path, err)
	}

	// Set socket permissions: owner+group read/write, no other.
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	// Set group ownership to "agent-users" if the group exists.
	if grp, err := user.LookupGroup("agent-users"); err == nil {
		if gid, err := strconv.Atoi(grp.Gid); err == nil {
			_ = syscall.Chown(path, -1, gid)
		}
	}

	return ln, nil
}
