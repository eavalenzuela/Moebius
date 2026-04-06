package executor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/agent/cdm"
	"github.com/eavalenzuela/Moebius/shared/protocol"
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

func TestExecuteInventoryFull_NilCollector(t *testing.T) {
	e := &Executor{inventory: nil}
	result := e.executeInventoryFull()
	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.Message != "inventory collector not configured" {
		t.Errorf("unexpected message: %s", result.Message)
	}
}

func TestExecute_InventoryFullType(t *testing.T) {
	// With nil inventory, the inventory_full path should return failed
	e := &Executor{}
	result := e.execute(context.Background(), protocol.JobDispatch{
		JobID: "test",
		Type:  "inventory_full",
	})
	if result.Status != "failed" {
		t.Fatalf("expected failed for inventory_full with nil collector, got %s", result.Status)
	}
}

// --- CDM gate tests (security invariant I2) ---

// newTestExecutor builds an Executor wired to a counting test server. It
// returns the executor, a pointer to the atomic request counter, and a
// teardown func.
func newTestExecutor(t *testing.T, cdmMgr *cdm.Manager) (ex *Executor, hitCount *int64, cleanup func()) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := New(srv.URL, srv.Client(), nil, cdmMgr, t.TempDir(), log)
	return e, &hits, srv.Close
}

// TestRunJob_CDMGate_HoldsWhenNoSession verifies invariant I2: when CDM is
// enabled and no session is active, runJob must not execute the job and must
// not make any HTTP calls to the server (no ack, no result). A server cannot
// bypass this by dispatching the job — the agent refuses on its own.
func TestRunJob_CDMGate_HoldsWhenNoSession(t *testing.T) {
	dir := t.TempDir()
	audit := cdm.NewAuditLog(dir + "/audit.log")
	m, err := cdm.New(dir+"/cdm.json", audit)
	if err != nil {
		t.Fatalf("create cdm manager: %v", err)
	}
	if err := m.Enable("test"); err != nil {
		t.Fatalf("enable cdm: %v", err)
	}
	// Intentionally NO GrantSession — CanExecuteJob() should return false.
	if m.CanExecuteJob() {
		t.Fatal("precondition: CanExecuteJob should be false with CDM enabled + no session")
	}

	e, hits, teardown := newTestExecutor(t, m)
	defer teardown()

	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo should-not-run"})
	e.runJob(context.Background(), protocol.JobDispatch{
		JobID:   "job-held",
		Type:    "exec",
		Payload: payload,
	})

	// runJob is synchronous when called directly. No background goroutine to wait for.
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Fatalf("expected 0 HTTP calls to server, got %d — CDM gate allowed the job through", got)
	}
}

// TestRunJob_CDMGate_AllowsWhenSessionActive verifies the positive path: with
// CDM enabled and an active session, runJob executes the job and reports the
// result. This is the companion to the hold test — it proves the gate is
// actually gating (not failing closed unconditionally).
func TestRunJob_CDMGate_AllowsWhenSessionActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Keep the exec payload portable; echo works on both but quoting differs.
		t.Skip("skip on windows: test uses sh-style echo")
	}
	dir := t.TempDir()
	audit := cdm.NewAuditLog(dir + "/audit.log")
	m, err := cdm.New(dir+"/cdm.json", audit)
	if err != nil {
		t.Fatalf("create cdm manager: %v", err)
	}
	if err := m.Enable("test"); err != nil {
		t.Fatalf("enable cdm: %v", err)
	}
	if err := m.GrantSession("tech", 10*time.Minute); err != nil {
		t.Fatalf("grant session: %v", err)
	}

	e, hits, teardown := newTestExecutor(t, m)
	defer teardown()

	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo ok"})
	e.runJob(context.Background(), protocol.JobDispatch{
		JobID:   "job-allowed",
		Type:    "exec",
		Payload: payload,
	})

	// Expect exactly 2 HTTP calls: acknowledge + result.
	if got := atomic.LoadInt64(hits); got != 2 {
		t.Fatalf("expected 2 HTTP calls (ack + result), got %d", got)
	}
}

// TestRunJob_CDMGate_NilManagerRefuses verifies the fail-closed path: if the
// CDM manager is somehow nil (construction bug, regression), runJob must
// refuse to execute. Never fail-open for a security invariant.
func TestRunJob_CDMGate_NilManagerRefuses(t *testing.T) {
	e, hits, teardown := newTestExecutor(t, nil)
	defer teardown()

	payload, _ := json.Marshal(protocol.ExecPayload{Command: "echo should-not-run"})
	e.runJob(context.Background(), protocol.JobDispatch{
		JobID:   "job-no-cdm",
		Type:    "exec",
		Payload: payload,
	})

	if got := atomic.LoadInt64(hits); got != 0 {
		t.Fatalf("expected 0 HTTP calls with nil CDM manager (fail-closed), got %d", got)
	}
}
