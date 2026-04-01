package localui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/agent/cdm"
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

func setupTestServer(t *testing.T) (*Server, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	cdmAudit := cdm.NewAuditLog(dir + "/cdm-audit.log")
	cdmMgr, err := cdm.New(dir+"/cdm.json", cdmAudit)
	if err != nil {
		t.Fatalf("create CDM: %v", err)
	}

	auth := &mockAuth{users: map[string]string{"admin": "pass123"}}
	sessions := localauth.NewSessionManager()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := NewServer(
		ServerConfig{Port: 0, DataDir: dir, LogDir: dir},
		auth,
		sessions,
		cdmMgr,
		nil, // audit logger (nil for tests)
		log,
		"test-agent-id",
		"https://server.example.com",
	)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = srv.Serve(ctx) // error expected on shutdown
	}()

	// Wait for server to be ready.
	select {
	case <-srv.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start within 5s")
	}

	return srv, cancel
}

func testClient(t *testing.T, srv *Server) *http.Client {
	t.Helper()
	// Load the test CA cert for TLS verification.
	caCertPEM, err := os.ReadFile(srv.CACertPath())
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	block, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				ServerName: "localhost",
				MinVersion: tls.VersionTLS12,
			},
		},
		Jar: jar,
	}
}

func TestServerLoginLogout(t *testing.T) {
	srv, cancel := setupTestServer(t)
	defer cancel()

	client := testClient(t, srv)
	base := "https://" + srv.Addr()

	// Unauthenticated request should fail.
	resp, err := client.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}

	// Login.
	loginBody := `{"username":"admin","password":"pass123"}`
	resp, err = client.Post(base+"/api/login", "application/json",
		jsonReader(loginBody))
	if err != nil {
		t.Fatalf("POST /api/login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}

	// Authenticated request should succeed.
	resp, err = client.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status (authed): %v", err)
	}
	var status map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&status)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if status["agent_id"] != "test-agent-id" {
		t.Errorf("agent_id = %v, want test-agent-id", status["agent_id"])
	}

	// Logout.
	resp, err = client.Post(base+"/api/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/logout: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("logout status = %d, want 200", resp.StatusCode)
	}

	// After logout, should be 401 again.
	resp, err = client.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status (after logout): %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status after logout = %d, want 401", resp.StatusCode)
	}
}

func TestServerBadLogin(t *testing.T) {
	srv, cancel := setupTestServer(t)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // test only
			},
		},
	}
	base := "https://" + srv.Addr()

	resp, err := client.Post(base+"/api/login", "application/json",
		jsonReader(`{"username":"admin","password":"wrong"}`))
	if err != nil {
		t.Fatalf("POST /api/login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestServerCDMFlow(t *testing.T) {
	srv, cancel := setupTestServer(t)
	defer cancel()

	client := testClient(t, srv)
	base := "https://" + srv.Addr()

	// Login.
	resp, err := client.Post(base+"/api/login", "application/json",
		jsonReader(`{"username":"admin","password":"pass123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()

	// Enable CDM.
	resp, err = client.Post(base+"/api/cdm/enable", "application/json", nil)
	if err != nil {
		t.Fatalf("enable CDM: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable CDM status = %d", resp.StatusCode)
	}

	// Check CDM status.
	resp, err = client.Get(base + "/api/cdm")
	if err != nil {
		t.Fatalf("get CDM: %v", err)
	}
	var cdmStatus map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&cdmStatus)
	_ = resp.Body.Close()
	if cdmStatus["enabled"] != true {
		t.Errorf("CDM enabled = %v, want true", cdmStatus["enabled"])
	}

	// Grant session.
	resp, err = client.Post(base+"/api/cdm/grant", "application/json",
		jsonReader(`{"duration":"10m"}`))
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grant status = %d", resp.StatusCode)
	}

	// Check session is active.
	resp, err = client.Get(base + "/api/cdm")
	if err != nil {
		t.Fatalf("get CDM after grant: %v", err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&cdmStatus)
	_ = resp.Body.Close()
	if cdmStatus["session_active"] != true {
		t.Errorf("session_active = %v, want true", cdmStatus["session_active"])
	}

	// Revoke session.
	resp, err = client.Post(base+"/api/cdm/revoke", "application/json", nil)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d", resp.StatusCode)
	}

	// Check session is no longer active.
	resp, err = client.Get(base + "/api/cdm")
	if err != nil {
		t.Fatalf("get CDM after revoke: %v", err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&cdmStatus)
	_ = resp.Body.Close()
	if cdmStatus["session_active"] != false {
		t.Errorf("session_active after revoke = %v, want false", cdmStatus["session_active"])
	}
}

func TestServerStaticFiles(t *testing.T) {
	srv, cancel := setupTestServer(t)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // test only
			},
		},
	}
	base := "https://" + srv.Addr()

	// Should serve index.html.
	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func jsonReader(s string) io.Reader {
	return strings.NewReader(s)
}
