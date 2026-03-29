package renewal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"log/slog"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

func TestNeedsRenewal_NotYet(t *testing.T) {
	path := generateCertWithExpiry(t, time.Now().Add(60*24*time.Hour)) // 60 days out
	needs, expiresAt, err := NeedsRenewal(path)
	if err != nil {
		t.Fatalf("NeedsRenewal: %v", err)
	}
	if needs {
		t.Error("should not need renewal with 60 days remaining")
	}
	if expiresAt.IsZero() {
		t.Error("expiresAt should not be zero")
	}
}

func TestNeedsRenewal_Within30Days(t *testing.T) {
	path := generateCertWithExpiry(t, time.Now().Add(15*24*time.Hour)) // 15 days out
	needs, _, err := NeedsRenewal(path)
	if err != nil {
		t.Fatalf("NeedsRenewal: %v", err)
	}
	if !needs {
		t.Error("should need renewal with 15 days remaining")
	}
}

func TestNeedsRenewal_Expired(t *testing.T) {
	path := generateCertWithExpiry(t, time.Now().Add(-1*time.Hour)) // already expired
	needs, _, err := NeedsRenewal(path)
	if err != nil {
		t.Fatalf("NeedsRenewal: %v", err)
	}
	if !needs {
		t.Error("should need renewal when already expired")
	}
}

func TestNeedsRenewal_MissingFile(t *testing.T) {
	_, _, err := NeedsRenewal("/nonexistent/cert.pem")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNeedsRenewal_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(path, []byte("not a cert"), 0o600)

	_, _, err := NeedsRenewal(path)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestRenew_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents/renew" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var req protocol.RenewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.CSR == "" {
			t.Error("CSR should not be empty")
		}

		resp := protocol.RenewResponse{
			Certificate: "-----BEGIN CERTIFICATE-----\nnew-cert\n-----END CERTIFICATE-----\n",
			CAChain:     "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	result, err := Renew(server.URL, server.Client(), log)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if result.CertPEM == "" {
		t.Error("CertPEM should not be empty")
	}
	if result.KeyPEM == "" {
		t.Error("KeyPEM should not be empty")
	}
}

func TestRenew_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := Renew(server.URL, server.Client(), log)
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestSaveRenewal(t *testing.T) {
	dir := t.TempDir()
	result := &Result{
		CertPEM:    "new-cert",
		KeyPEM:     "new-key",
		CAChainPEM: "new-ca",
	}
	err := SaveRenewal(result,
		filepath.Join(dir, "client.crt"),
		filepath.Join(dir, "client.key"),
		filepath.Join(dir, "ca.crt"),
	)
	if err != nil {
		t.Fatalf("SaveRenewal: %v", err)
	}

	assertContent(t, filepath.Join(dir, "client.crt"), "new-cert")
	assertContent(t, filepath.Join(dir, "client.key"), "new-key")
	assertContent(t, filepath.Join(dir, "ca.crt"), "new-ca")
}

func TestRetryBackoff(t *testing.T) {
	if RetryBackoff(0) != 5*time.Minute {
		t.Errorf("attempt 0 = %v, want 5m", RetryBackoff(0))
	}
	if RetryBackoff(1) != 15*time.Minute {
		t.Errorf("attempt 1 = %v, want 15m", RetryBackoff(1))
	}
	if RetryBackoff(2) != 1*time.Hour {
		t.Errorf("attempt 2 = %v, want 1h", RetryBackoff(2))
	}
	if RetryBackoff(3) != 4*time.Hour {
		t.Errorf("attempt 3 = %v, want 4h", RetryBackoff(3))
	}
	// Beyond schedule, should return last value
	if RetryBackoff(10) != 4*time.Hour {
		t.Errorf("attempt 10 = %v, want 4h", RetryBackoff(10))
	}
	// Negative should be clamped
	if RetryBackoff(-1) != 5*time.Minute {
		t.Errorf("attempt -1 = %v, want 5m", RetryBackoff(-1))
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test helper with controlled paths
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", path, string(data), want)
	}
}

func generateCertWithExpiry(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	path := filepath.Join(t.TempDir(), "cert.pem")
	_ = os.WriteFile(path, certPEM, 0o600)
	return path
}
