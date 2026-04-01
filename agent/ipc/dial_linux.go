//go:build linux

package ipc

import (
	"fmt"
	"net"
)

// dial connects to the Unix domain socket at path.
func dial(path string) (net.Conn, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial unix %s: %w", path, err)
	}
	return conn, nil
}
