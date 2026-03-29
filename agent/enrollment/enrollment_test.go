package enrollment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"log/slog"

	"github.com/moebius-oss/moebius/shared/protocol"
)

func TestEnroll_Success(t *testing.T) {
	// Set up a mock server that accepts enrollment
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/enroll" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req protocol.EnrollRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.EnrollmentToken != "test-token-123" {
			t.Errorf("token = %q, want %q", req.EnrollmentToken, "test-token-123")
		}
		if req.CSR == "" {
			t.Error("CSR should not be empty")
		}
		if req.Hostname == "" {
			t.Error("Hostname should not be empty")
		}

		resp := protocol.EnrollResponse{
			AgentID:             "agt_test123",
			Certificate:         "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n",
			CAChain:             "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n",
			PollIntervalSeconds: 30,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Write enrollment token to temp file
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "enrollment.token")
	_ = os.WriteFile(tokenPath, []byte("test-token-123"), 0o600)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	result, err := Enroll(server.URL, tokenPath, server.Client(), log)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	if result.AgentID != "agt_test123" {
		t.Errorf("AgentID = %q, want %q", result.AgentID, "agt_test123")
	}
	if result.CertPEM == "" {
		t.Error("CertPEM should not be empty")
	}
	if result.KeyPEM == "" {
		t.Error("KeyPEM should not be empty")
	}
	if result.CAChainPEM == "" {
		t.Error("CAChainPEM should not be empty")
	}
	if result.PollIntervalSeconds != 30 {
		t.Errorf("PollIntervalSeconds = %d, want 30", result.PollIntervalSeconds)
	}
}

func TestEnroll_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "enrollment.token")
	_ = os.WriteFile(tokenPath, []byte("bad-token"), 0o600)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := Enroll(server.URL, tokenPath, server.Client(), log)
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestEnroll_MissingToken(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := Enroll("http://localhost", "/nonexistent/token", http.DefaultClient, log)
	if err == nil {
		t.Fatal("expected error for missing token file")
	}
}

func TestEnroll_EmptyToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "enrollment.token")
	_ = os.WriteFile(tokenPath, []byte("  \n"), 0o600)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := Enroll("http://localhost", tokenPath, http.DefaultClient, log)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestSaveCredentials(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "enrollment.token")
	_ = os.WriteFile(tokenPath, []byte("token"), 0o600)

	result := &Result{
		AgentID:    "agt_test",
		CertPEM:    "cert-data",
		KeyPEM:     "key-data",
		CAChainPEM: "ca-data",
	}

	err := SaveCredentials(result,
		filepath.Join(dir, "client.crt"),
		filepath.Join(dir, "client.key"),
		filepath.Join(dir, "ca.crt"),
		filepath.Join(dir, "agent_id"),
		tokenPath,
	)
	if err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	// Verify files written
	assertFileContent(t, filepath.Join(dir, "client.crt"), "cert-data")
	assertFileContent(t, filepath.Join(dir, "client.key"), "key-data")
	assertFileContent(t, filepath.Join(dir, "ca.crt"), "ca-data")
	assertFileContent(t, filepath.Join(dir, "agent_id"), "agt_test")

	// Token should be deleted
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Error("enrollment token should have been deleted")
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test helper with controlled paths
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("%s content = %q, want %q", path, string(data), want)
	}
}
