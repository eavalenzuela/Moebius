//go:build linux

package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadRequest(t *testing.T) {
	var buf bytes.Buffer

	req := &Request{
		ID:     "1",
		Method: "test.echo",
		Params: json.RawMessage(`{"msg":"hello"}`),
	}

	if err := WriteMessage(&buf, req); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	scanner := bufio.NewScanner(&buf)
	got, err := ReadRequest(scanner)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}

	if got.ID != req.ID {
		t.Errorf("ID = %q, want %q", got.ID, req.ID)
	}
	if got.Method != req.Method {
		t.Errorf("Method = %q, want %q", got.Method, req.Method)
	}
	if string(got.Params) != string(req.Params) {
		t.Errorf("Params = %s, want %s", got.Params, req.Params)
	}
}

func TestWriteAndReadResponse(t *testing.T) {
	var buf bytes.Buffer

	resp := &Response{
		ID:     "42",
		Result: json.RawMessage(`{"ok":true}`),
	}

	if err := WriteMessage(&buf, resp); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	scanner := bufio.NewScanner(&buf)
	got, err := ReadResponse(scanner)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}

	if got.ID != resp.ID {
		t.Errorf("ID = %q, want %q", got.ID, resp.ID)
	}
	if string(got.Result) != string(resp.Result) {
		t.Errorf("Result = %s, want %s", got.Result, resp.Result)
	}
	if got.Error != nil {
		t.Errorf("Error = %v, want nil", got.Error)
	}
}

func TestRouterDispatch(t *testing.T) {
	router := NewRouter()

	type echoParams struct {
		Msg string `json:"msg"`
	}
	type echoResult struct {
		Echo string `json:"echo"`
	}

	router.Handle("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var p echoParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
		}
		return echoResult{Echo: p.Msg}, nil
	})

	t.Run("known method", func(t *testing.T) {
		req := &Request{
			ID:     "1",
			Method: "echo",
			Params: json.RawMessage(`{"msg":"hi"}`),
		}
		resp := router.Dispatch(context.Background(), req)
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
		var result echoResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if result.Echo != "hi" {
			t.Errorf("Echo = %q, want %q", result.Echo, "hi")
		}
	})

	t.Run("unknown method", func(t *testing.T) {
		req := &Request{ID: "2", Method: "nope"}
		resp := router.Dispatch(context.Background(), req)
		if resp.Error == nil {
			t.Fatal("expected error for unknown method")
		}
		if resp.Error.Code != CodeMethodNotFound {
			t.Errorf("Code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
		}
	})

	t.Run("handler returns error", func(t *testing.T) {
		router.Handle("fail", func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, &Error{Code: CodeUnauthorized, Message: "denied"}
		})
		req := &Request{ID: "3", Method: "fail"}
		resp := router.Dispatch(context.Background(), req)
		if resp.Error == nil {
			t.Fatal("expected error")
		}
		if resp.Error.Code != CodeUnauthorized {
			t.Errorf("Code = %d, want %d", resp.Error.Code, CodeUnauthorized)
		}
	})
}

func TestServerClientRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	router := NewRouter()

	type addParams struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	type addResult struct {
		Sum int `json:"sum"`
	}

	router.Handle("add", func(_ context.Context, params json.RawMessage) (any, error) {
		var p addParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
		}
		return addResult{Sum: p.A + p.B}, nil
	})

	router.Handle("ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "pong"}, nil
	})

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := NewServer(sockPath, router, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srvErr := make(chan error, 1)
	go func() {
		srvErr <- srv.Serve(ctx)
	}()

	// Wait for listener to be ready.
	waitForSocket(t, sockPath)

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	t.Run("add", func(t *testing.T) {
		var result addResult
		if err := client.Call("add", addParams{A: 3, B: 4}, &result); err != nil {
			t.Fatalf("Call add: %v", err)
		}
		if result.Sum != 7 {
			t.Errorf("Sum = %d, want 7", result.Sum)
		}
	})

	t.Run("ping", func(t *testing.T) {
		var result map[string]string
		if err := client.Call("ping", nil, &result); err != nil {
			t.Fatalf("Call ping: %v", err)
		}
		if result["status"] != "pong" {
			t.Errorf("status = %q, want %q", result["status"], "pong")
		}
	})

	t.Run("unknown method", func(t *testing.T) {
		err := client.Call("nonexistent", nil, nil)
		if err == nil {
			t.Fatal("expected error for unknown method")
		}
		ipcErr, ok := err.(*Error)
		if !ok {
			t.Fatalf("expected *Error, got %T: %v", err, err)
		}
		if ipcErr.Code != CodeMethodNotFound {
			t.Errorf("Code = %d, want %d", ipcErr.Code, CodeMethodNotFound)
		}
	})

	t.Run("multiple sequential calls", func(t *testing.T) {
		for i := range 5 {
			var result addResult
			if err := client.Call("add", addParams{A: i, B: i}, &result); err != nil {
				t.Fatalf("Call %d: %v", i, err)
			}
			if result.Sum != i*2 {
				t.Errorf("Call %d: Sum = %d, want %d", i, result.Sum, i*2)
			}
		}
	})

	cancel()
	<-srv.Done()
}

func TestMultipleClients(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	router := NewRouter()
	router.Handle("ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "pong"}, nil
	})

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := NewServer(sockPath, router, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)

	// Connect multiple clients concurrently.
	const numClients = 5
	errs := make(chan error, numClients)
	for range numClients {
		go func() {
			c, err := NewClient(sockPath)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = c.Close() }()

			var result map[string]string
			errs <- c.Call("ping", nil, &result)
		}()
	}

	for range numClients {
		if err := <-errs; err != nil {
			t.Errorf("client error: %v", err)
		}
	}

	cancel()
	<-srv.Done()
}

// waitForSocket polls until the socket file exists or the timeout expires.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s not ready after 2s", path)
}
