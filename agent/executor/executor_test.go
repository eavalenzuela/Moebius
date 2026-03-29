package executor

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/moebius-oss/moebius/shared/protocol"
)

func TestExecuteExec_Success(t *testing.T) {
	e := &Executor{}
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo hello"})
	result := e.executeExec(context.Background(), payload)

	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s: %s", result.Status, result.Message)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %v", result.ExitCode)
	}
	if result.Stdout == "" {
		t.Error("expected stdout output")
	}
}

func TestExecuteExec_FailedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses sh syntax")
	}
	e := &Executor{}
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "exit 42"})
	result := e.executeExec(context.Background(), payload)

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.ExitCode == nil || *result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %v", result.ExitCode)
	}
}

func TestExecuteExec_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses sleep command")
	}
	e := &Executor{}
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "sleep 10", TimeoutSeconds: 1})
	result := e.executeExec(context.Background(), payload)

	if result.Status != "timed_out" {
		t.Fatalf("expected timed_out, got %s: %s", result.Status, result.Message)
	}
}

func TestExecuteExec_EmptyCommand(t *testing.T) {
	e := &Executor{}
	payload, _ := json.Marshal(protocol.ExecPayload{Command: ""})
	result := e.executeExec(context.Background(), payload)

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.Message != "empty command" {
		t.Errorf("expected 'empty command', got %q", result.Message)
	}
}

func TestExecuteExec_InvalidPayload(t *testing.T) {
	e := &Executor{}
	result := e.executeExec(context.Background(), []byte("not json"))

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecute_UnsupportedType(t *testing.T) {
	e := &Executor{}
	result := e.execute(context.Background(), protocol.JobDispatch{
		JobID: "test",
		Type:  "unknown_type",
	})

	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
}

func TestExecuteExec_StderrCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses sh syntax")
	}
	e := &Executor{}
	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo error >&2"})
	result := e.executeExec(context.Background(), payload)

	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s: %s", result.Status, result.Message)
	}
	if result.Stderr == "" {
		t.Error("expected stderr output")
	}
}
