//go:build windows

package ipc

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// createListener creates a Windows named pipe at path with an ACL that
// restricts access to SYSTEM and local Administrators.
func createListener(path string) (net.Listener, error) {
	// SDDL: D — DACL
	//   (A;;GA;;;SY) — Allow Generic All to SYSTEM
	//   (A;;GA;;;BA) — Allow Generic All to Built-in Administrators
	sddl := "D:(A;;GA;;;SY)(A;;GA;;;BA)"

	cfg := &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false, // byte stream
	}

	ln, err := winio.ListenPipe(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("listen pipe %s: %w", path, err)
	}
	return ln, nil
}
