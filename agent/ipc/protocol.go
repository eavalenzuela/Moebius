// Package ipc implements the local IPC transport between the agent daemon
// and local CLI / web UI clients. Communication uses newline-delimited JSON
// messages over a Unix socket (Linux) or named pipe (Windows).
//
// The wire protocol is a simplified JSON-RPC 2.0:
//
//	Request:  {"id":"...","method":"...","params":{...}}
//	Response: {"id":"...","result":{...}} or {"id":"...","error":{"code":N,"message":"..."}}
package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Request is a JSON-RPC-style request sent by the client.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	Token  string          `json:"token,omitempty"` // session token for authenticated methods
}

// Response is a JSON-RPC-style response returned by the server.
type Response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error represents an error in a Response.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("ipc error %d: %s", e.Code, e.Message)
}

// Standard error codes.
const (
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeUnauthorized   = -32001
)

// WriteMessage serializes v as JSON and writes it as a single newline-terminated
// line to w.
func WriteMessage(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ReadRequest reads a single newline-delimited JSON request from the scanner.
func ReadRequest(scanner *bufio.Scanner) (*Request, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	return &req, nil
}

// ReadResponse reads a single newline-delimited JSON response from the scanner.
func ReadResponse(scanner *bufio.Scanner) (*Response, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}
