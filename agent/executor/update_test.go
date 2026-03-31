package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/eavalenzuela/Moebius/agent/update"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// mockPlatform implements platform.Platform for testing update paths.
type mockPlatform struct {
	dir string
}

func (p *mockPlatform) ConfigDir() string           { return p.dir }
func (p *mockPlatform) BinaryDir() string           { return p.dir }
func (p *mockPlatform) DataDir() string             { return p.dir }
func (p *mockPlatform) LogDir() string              { return p.dir }
func (p *mockPlatform) RuntimeDir() string          { return p.dir }
func (p *mockPlatform) ConfigPath() string          { return filepath.Join(p.dir, "config.toml") }
func (p *mockPlatform) EnrollmentTokenPath() string { return filepath.Join(p.dir, "enrollment.token") }
func (p *mockPlatform) CACertPath() string          { return filepath.Join(p.dir, "ca.crt") }
func (p *mockPlatform) ClientCertPath() string      { return filepath.Join(p.dir, "client.crt") }
func (p *mockPlatform) ClientKeyPath() string       { return filepath.Join(p.dir, "client.key") }
func (p *mockPlatform) SocketPath() string          { return filepath.Join(p.dir, "agent.sock") }
func (p *mockPlatform) AgentIDPath() string         { return filepath.Join(p.dir, "agent_id") }
func (p *mockPlatform) CDMStatePath() string        { return filepath.Join(p.dir, "cdm.json") }
func (p *mockPlatform) CDMAuditLogPath() string     { return filepath.Join(p.dir, "cdm-audit.log") }
func (p *mockPlatform) DropDir() string             { return filepath.Join(p.dir, "drop") }
func (p *mockPlatform) BinaryPath() string          { return filepath.Join(p.dir, "moebius-agent") }
func (p *mockPlatform) BinaryStagingPath() string   { return filepath.Join(p.dir, "moebius-agent.new") }
func (p *mockPlatform) BinaryPreviousPath() string {
	return filepath.Join(p.dir, "moebius-agent.previous")
}
func (p *mockPlatform) PendingUpdatePath() string { return filepath.Join(p.dir, "pending_update.json") }
func (p *mockPlatform) ServiceName() string       { return "test-agent" }

func TestExecuteAgentUpdate_InvalidPayload(t *testing.T) {
	e := &Executor{log: slog.Default()}
	result := e.executeAgentUpdate(context.Background(), "job_1", []byte("not json"))
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestExecuteAgentUpdate_MissingVersion(t *testing.T) {
	e := &Executor{log: slog.Default()}
	payload, _ := json.Marshal(protocol.AgentUpdatePayload{})
	result := e.executeAgentUpdate(context.Background(), "job_1", payload)
	if result.Status != "failed" || result.Message != "version is required in update payload" {
		t.Errorf("got status=%s message=%s", result.Status, result.Message)
	}
}

func TestExecuteAgentUpdate_NoPlatform(t *testing.T) {
	e := &Executor{log: slog.Default()}
	payload, _ := json.Marshal(protocol.AgentUpdatePayload{
		Version: "2.0.0",
		Force:   true,
	})
	result := e.executeAgentUpdate(context.Background(), "job_1", payload)
	if result.Status != "failed" {
		t.Errorf("expected failed (no platform), got %s: %s", result.Status, result.Message)
	}
}

func TestExecuteAgentUpdate_FullFlow(t *testing.T) {
	dir := t.TempDir()
	plat := &mockPlatform{dir: dir}

	// Create a "current binary"
	currentBinary := []byte("old-agent-binary-v1.0")
	if err := os.WriteFile(plat.BinaryPath(), currentBinary, 0o755); err != nil {
		t.Fatal(err)
	}

	// Generate Ed25519 key pair
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	// New binary content
	newBinary := []byte("new-agent-binary-v2.0")
	binHash := sha256.Sum256(newBinary)
	binHashHex := fmt.Sprintf("%x", binHash)

	// Sign the SHA-256 hash
	sig := ed25519.Sign(privKey, binHash[:])
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	// PEM-encode public key
	pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	// Mock server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data/new-binary":
			_, _ = w.Write(newBinary)
		case "/v1/agents/signing-keys/sgk_test1":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"public_key": pubPEM})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	e := &Executor{
		serverURL:    srv.URL,
		client:       srv.Client(),
		log:          slog.Default(),
		platform:     plat,
		pollInterval: 30,
	}

	payload, _ := json.Marshal(protocol.AgentUpdatePayload{
		Version:        "2.0.0",
		DownloadURL:    srv.URL + "/data/new-binary",
		SHA256:         binHashHex,
		Signature:      sigB64,
		SignatureKeyID: "sgk_test1",
		SizeBytes:      int64(len(newBinary)),
		Force:          true,
	})

	result := e.executeAgentUpdate(context.Background(), "job_update1", payload)
	if result.Status != "restarting" {
		t.Fatalf("expected restarting, got %s: %s", result.Status, result.Message)
	}

	// Verify binary was replaced
	data, err := os.ReadFile(plat.BinaryPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(newBinary) {
		t.Error("binary was not replaced with new version")
	}

	// Verify previous binary was saved
	prevData, err := os.ReadFile(plat.BinaryPreviousPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(prevData) != string(currentBinary) {
		t.Error("previous binary not saved correctly")
	}

	// Verify pending_update.json was written
	pending, err := update.ReadPending(plat.PendingUpdatePath())
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil {
		t.Fatal("pending_update.json not written")
	}
	if pending.JobID != "job_update1" {
		t.Errorf("pending job_id: got %s, want job_update1", pending.JobID)
	}
	if pending.ExpectedVersion != "2.0.0" {
		t.Errorf("expected_version: got %s, want 2.0.0", pending.ExpectedVersion)
	}
}

func TestExecuteAgentUpdate_ChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	plat := &mockPlatform{dir: dir}

	if err := os.WriteFile(plat.BinaryPath(), []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong content"))
	}))
	defer srv.Close()

	e := &Executor{
		serverURL: srv.URL,
		client:    srv.Client(),
		log:       slog.Default(),
		platform:  plat,
	}

	payload, _ := json.Marshal(protocol.AgentUpdatePayload{
		Version:        "2.0.0",
		DownloadURL:    srv.URL + "/binary",
		SHA256:         "0000000000000000000000000000000000000000000000000000000000000000",
		Signature:      "dGVzdA==",
		SignatureKeyID: "sgk_test",
		Force:          true,
	})

	result := e.executeAgentUpdate(context.Background(), "job_1", payload)
	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.Message == "" {
		t.Error("expected error message about checksum")
	}

	// Verify staging file was cleaned up
	if _, err := os.Stat(plat.BinaryStagingPath()); !os.IsNotExist(err) {
		t.Error("staging file should have been cleaned up")
	}
}

func TestExecuteAgentRollback_NoPlatform(t *testing.T) {
	e := &Executor{log: slog.Default()}
	payload, _ := json.Marshal(AgentRollbackPayload{Reason: "test"})
	result := e.executeAgentRollback(context.Background(), payload)
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestExecuteAgentRollback_NoPrevious(t *testing.T) {
	dir := t.TempDir()
	plat := &mockPlatform{dir: dir}

	if err := os.WriteFile(plat.BinaryPath(), []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	e := &Executor{log: slog.Default(), platform: plat}
	payload, _ := json.Marshal(AgentRollbackPayload{Reason: "test"})
	result := e.executeAgentRollback(context.Background(), payload)
	if result.Status != "failed" {
		t.Errorf("expected failed (no previous), got %s: %s", result.Status, result.Message)
	}
}

func TestExecuteAgentRollback_Success(t *testing.T) {
	dir := t.TempDir()
	plat := &mockPlatform{dir: dir}

	if err := os.WriteFile(plat.BinaryPath(), []byte("new-bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plat.BinaryPreviousPath(), []byte("old-good"), 0o755); err != nil {
		t.Fatal(err)
	}

	e := &Executor{log: slog.Default(), platform: plat}
	payload, _ := json.Marshal(AgentRollbackPayload{Reason: "runtime issue"})
	result := e.executeAgentRollback(context.Background(), payload)
	if result.Status != "restarting" {
		t.Fatalf("expected restarting, got %s: %s", result.Status, result.Message)
	}

	// Verify binary was restored
	data, err := os.ReadFile(plat.BinaryPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-good" {
		t.Errorf("expected old-good, got %s", string(data))
	}
}
