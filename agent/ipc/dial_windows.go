//go:build windows

package ipc

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// dial connects to the Windows named pipe at path.
func dial(path string) (net.Conn, error) {
	conn, err := winio.DialPipe(path, nil)
	if err != nil {
		return nil, fmt.Errorf("dial pipe %s: %w", path, err)
	}
	return conn, nil
}
