package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Client connects to the agent's IPC socket/pipe and sends JSON-RPC requests.
type Client struct {
	conn    net.Conn
	scanner *bufio.Scanner
	mu      sync.Mutex // serializes writes and read pairs
	seq     atomic.Uint64
}

// NewClient connects to the IPC endpoint at path and returns a ready Client.
func NewClient(path string) (*Client, error) {
	conn, err := dial(path)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	return &Client{conn: conn, scanner: scanner}, nil
}

// Call invokes a method with the given params and unmarshals the result into
// dest. If dest is nil the result is discarded.
func (c *Client) Call(method string, params any, dest any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := fmt.Sprintf("%d", c.seq.Add(1))

	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		rawParams = p
	}

	req := &Request{
		ID:     id,
		Method: method,
		Params: rawParams,
	}

	if err := WriteMessage(c.conn, req); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	resp, err := ReadResponse(c.scanner)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.Error != nil {
		return resp.Error
	}

	if dest != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, dest); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return nil
}

// CallWithToken is like Call but includes a session token in the request.
func (c *Client) CallWithToken(method string, token string, params any, dest any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := fmt.Sprintf("%d", c.seq.Add(1))

	var rawParams json.RawMessage
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
		rawParams = p
	}

	req := &Request{
		ID:     id,
		Method: method,
		Params: rawParams,
		Token:  token,
	}

	if err := WriteMessage(c.conn, req); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	resp, err := ReadResponse(c.scanner)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.Error != nil {
		return resp.Error
	}

	if dest != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, dest); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
