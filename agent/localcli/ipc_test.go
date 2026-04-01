//go:build linux

package localcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/agent/cdm"
	agentconfig "github.com/eavalenzuela/Moebius/agent/config"
	"github.com/eavalenzuela/Moebius/agent/ipc"
	"github.com/eavalenzuela/Moebius/agent/localaudit"
	"github.com/eavalenzuela/Moebius/agent/localauth"
)

// mockAuth implements localauth.Authenticator for testing.
type mockAuth struct {
	users map[string]string
}

func (m *mockAuth) Authenticate(username, password string) error {
	if pw, ok := m.users[username]; ok && pw == password {
		return nil
	}
	return fmt.Errorf("invalid credentials")
}

// setupTestDaemon starts a full IPC server with auth + CLI methods.
// Returns the socket path, cancel func, and a done channel.
func setupTestDaemon(t *testing.T) (string, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// Create log file with some content.
	logFile := filepath.Join(dir, "agent.log")
	_ = os.WriteFile(logFile, []byte("line1\nline2\nline3\nline4\nline5\n"), 0o644) //nolint:gosec // test data

	// CDM setup.
	cdmAudit := cdm.NewAuditLog(filepath.Join(dir, "cdm-audit.log"))
	cdmMgr, err := cdm.New(filepath.Join(dir, "cdm.json"), cdmAudit)
	if err != nil {
		t.Fatalf("create CDM: %v", err)
	}

	// Build router with auth + CLI methods.
	auth := &mockAuth{users: map[string]string{"admin": "secret"}}
	sessions := localauth.NewSessionManager()
	router := ipc.NewRouter()

	audit := localaudit.New(filepath.Join(dir, "local-audit.log"))

	localauth.RegisterIPC(router, auth, sessions)

	requireAuth := RequireAuthMiddleware(sessions)
	RegisterIPC(router, &DaemonState{
		AgentID: "test-agent-123",
		Config: &agentconfig.Config{
			Server: agentconfig.ServerConfig{
				URL:                 "https://server.example.com",
				PollIntervalSeconds: 30,
			},
		},
		CDMManager: cdmMgr,
		AuditLog:   audit,
		LogFile:    logFile,
	}, requireAuth)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := ipc.NewServer(sockPath, router, log)

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = srv.Serve(ctx) }()

	// Wait for server.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := ipc.NewClient(sockPath)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return sockPath, cancel
}

// loginClient connects and authenticates, returning a ready CLI.
func loginClient(t *testing.T, sockPath string) *CLI {
	t.Helper()
	cli := New(sockPath)
	if err := cli.Login("admin", "secret"); err != nil {
		t.Fatalf("login: %v", err)
	}
	return cli
}

func TestIPCStatus(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	var result StatusResult
	if err := cli.authedCall("agent.status", nil, &result); err != nil {
		t.Fatalf("agent.status: %v", err)
	}

	if result.AgentID != "test-agent-123" {
		t.Errorf("AgentID = %q, want %q", result.AgentID, "test-agent-123")
	}
	if result.ServerURL != "https://server.example.com" {
		t.Errorf("ServerURL = %q", result.ServerURL)
	}
	if result.PollInterval != 30 {
		t.Errorf("PollInterval = %d, want 30", result.PollInterval)
	}
}

func TestIPCCDMFlow(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	// CDM should be disabled initially.
	var status CDMStatusResult
	if err := cli.authedCall("cdm.status", nil, &status); err != nil {
		t.Fatalf("cdm.status: %v", err)
	}
	if status.Enabled {
		t.Error("CDM should be disabled initially")
	}

	// Enable CDM.
	if err := cli.authedCall("cdm.enable", map[string]string{"actor": "admin"}, nil); err != nil {
		t.Fatalf("cdm.enable: %v", err)
	}

	// Verify enabled.
	if err := cli.authedCall("cdm.status", nil, &status); err != nil {
		t.Fatalf("cdm.status: %v", err)
	}
	if !status.Enabled {
		t.Error("CDM should be enabled")
	}

	// Grant session.
	if err := cli.authedCall("cdm.grant", map[string]string{
		"actor": "admin", "duration": "10m",
	}, nil); err != nil {
		t.Fatalf("cdm.grant: %v", err)
	}

	// Verify session active.
	if err := cli.authedCall("cdm.status", nil, &status); err != nil {
		t.Fatalf("cdm.status: %v", err)
	}
	if !status.SessionActive {
		t.Error("session should be active after grant")
	}

	// Revoke session.
	if err := cli.authedCall("cdm.revoke", map[string]string{"actor": "admin"}, nil); err != nil {
		t.Fatalf("cdm.revoke: %v", err)
	}

	// Verify session inactive.
	if err := cli.authedCall("cdm.status", nil, &status); err != nil {
		t.Fatalf("cdm.status: %v", err)
	}
	if status.SessionActive {
		t.Error("session should be inactive after revoke")
	}

	// Disable CDM.
	if err := cli.authedCall("cdm.disable", map[string]string{"actor": "admin"}, nil); err != nil {
		t.Fatalf("cdm.disable: %v", err)
	}
}

func TestIPCLogs(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	var result LogsResult
	if err := cli.authedCall("agent.logs", LogsParams{Tail: 3}, &result); err != nil {
		t.Fatalf("agent.logs: %v", err)
	}

	if len(result.Lines) != 3 {
		t.Fatalf("Lines count = %d, want 3", len(result.Lines))
	}
	if result.Lines[0] != "line3" {
		t.Errorf("Lines[0] = %q, want %q", result.Lines[0], "line3")
	}
}

func TestIPCAudit(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	// Enable CDM to generate an audit entry.
	_ = cli.authedCall("cdm.enable", map[string]string{"actor": "admin"}, nil)

	var result AuditResult
	if err := cli.authedCall("agent.audit", nil, &result); err != nil {
		t.Fatalf("agent.audit: %v", err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one audit entry")
	}
	if result.Entries[0].Action != localaudit.ActionCDMToggle {
		t.Errorf("Action = %q, want %q", result.Entries[0].Action, localaudit.ActionCDMToggle)
	}
}

func TestIPCRequiresAuth(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	// Connect without logging in.
	cli := New(sockPath)
	defer cli.Close()
	if err := cli.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Calling agent.status without a token should fail.
	err := cli.client.Call("agent.status", nil, nil)
	if err == nil {
		t.Fatal("expected error for unauthenticated call")
	}
	ipcErr, ok := err.(*ipc.Error)
	if !ok {
		t.Fatalf("expected *ipc.Error, got %T: %v", err, err)
	}
	if ipcErr.Code != ipc.CodeUnauthorized {
		t.Errorf("Code = %d, want %d", ipcErr.Code, ipc.CodeUnauthorized)
	}
}

func TestIPCBadToken(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := New(sockPath)
	defer cli.Close()
	if err := cli.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Call with a fake token.
	err := cli.client.CallWithToken("agent.status", "bogus-token", nil, nil)
	if err == nil {
		t.Fatal("expected error for bad token")
	}
	ipcErr, ok := err.(*ipc.Error)
	if !ok {
		t.Fatalf("expected *ipc.Error, got %T: %v", err, err)
	}
	if ipcErr.Code != ipc.CodeUnauthorized {
		t.Errorf("Code = %d, want %d", ipcErr.Code, ipc.CodeUnauthorized)
	}
}

func TestCLILoginRoundTrip(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := New(sockPath)
	defer cli.Close()

	// Login should succeed.
	if err := cli.Login("admin", "secret"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Token should be set.
	if cli.token == "" {
		t.Error("token is empty after login")
	}

	// Should be able to call authed methods.
	var result StatusResult
	if err := cli.authedCall("agent.status", nil, &result); err != nil {
		t.Fatalf("authedCall: %v", err)
	}

	// Verify we get real data back.
	if result.AgentID != "test-agent-123" {
		t.Errorf("AgentID = %q", result.AgentID)
	}
}

func TestCLILoginBadCredentials(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := New(sockPath)
	defer cli.Close()

	err := cli.Login("admin", "wrong")
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestRunStatusOutput(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	// RunStatus writes to stdout; just verify it doesn't error.
	if err := cli.RunStatus(); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
}

func TestRunCDMStatusOutput(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	if err := cli.RunCDMStatus(); err != nil {
		t.Fatalf("RunCDMStatus: %v", err)
	}
}

func TestRunLogsOutput(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	if err := cli.RunLogs(5); err != nil {
		t.Fatalf("RunLogs: %v", err)
	}
}

func TestRunAuditOutput(t *testing.T) {
	sockPath, cancel := setupTestDaemon(t)
	defer cancel()

	cli := loginClient(t, sockPath)
	defer cli.Close()

	// Empty audit initially.
	if err := cli.RunAudit(); err != nil {
		t.Fatalf("RunAudit: %v", err)
	}
}

// Suppress unused import warning for json.
var _ = json.Marshal
