package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCertProvider_LoadAndSwap(t *testing.T) {
	dir := t.TempDir()
	certPath1, keyPath1 := generateTestCert(t, dir, "cert1.pem", "key1.pem", "agent-1")
	certPath2, keyPath2 := generateTestCert(t, dir, "cert2.pem", "key2.pem", "agent-2")

	cp, err := NewCertProvider(certPath1, keyPath1)
	if err != nil {
		t.Fatalf("NewCertProvider: %v", err)
	}

	cert1, err := cp.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert1 == nil {
		t.Fatal("cert1 is nil")
	}

	// Swap to cert2
	if err := cp.Swap(certPath2, keyPath2); err != nil {
		t.Fatalf("Swap: %v", err)
	}

	cert2, err := cp.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after swap: %v", err)
	}
	if cert1 == cert2 {
		t.Error("expected different certificate after swap")
	}
}

func TestCertProvider_InvalidPath(t *testing.T) {
	_, err := NewCertProvider("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Fatal("expected error for nonexistent files")
	}
}

func TestCertProvider_SwapInvalidPath(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateTestCert(t, dir, "cert.pem", "key.pem", "agent-1")

	cp, err := NewCertProvider(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertProvider: %v", err)
	}

	err = cp.Swap("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Fatal("expected error for invalid swap path")
	}

	// Original cert should still be valid
	cert, err := cp.GetCertificate(nil)
	if err != nil || cert == nil {
		t.Error("original cert should still be available after failed swap")
	}
}

func TestLoadCAPool(t *testing.T) {
	dir := t.TempDir()
	caPath := generateTestCA(t, dir, "ca.pem")

	pool, err := LoadCAPool(caPath)
	if err != nil {
		t.Fatalf("LoadCAPool: %v", err)
	}
	if pool == nil {
		t.Fatal("pool is nil")
	}
}

func TestLoadCAPool_InvalidPath(t *testing.T) {
	_, err := LoadCAPool("/nonexistent/ca.pem")
	if err == nil {
		t.Fatal("expected error for nonexistent CA")
	}
}

func TestLoadCAPool_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(path, []byte("not a certificate"), 0o600)

	_, err := LoadCAPool(path)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestNewTLSConfig(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateTestCert(t, dir, "cert.pem", "key.pem", "agent-1")
	caPath := generateTestCA(t, dir, "ca.pem")

	cp, err := NewCertProvider(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewCertProvider: %v", err)
	}
	pool, err := LoadCAPool(caPath)
	if err != nil {
		t.Fatalf("LoadCAPool: %v", err)
	}

	cfg := NewTLSConfig(cp, pool)
	if cfg.GetClientCertificate == nil {
		t.Error("GetClientCertificate should be set")
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs should be set")
	}
	if cfg.MinVersion != 0x0303 { // TLS 1.2
		t.Errorf("MinVersion = %#x, want TLS 1.2", cfg.MinVersion)
	}
}

// generateTestCert creates a self-signed certificate and key for testing.
func generateTestCert(t *testing.T, dir, certFile, keyFile, cn string) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPath = filepath.Join(dir, certFile)
	keyPath = filepath.Join(dir, keyFile)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	return certPath, keyPath
}

// generateTestCA creates a self-signed CA cert for testing.
func generateTestCA(t *testing.T, dir, certFile string) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, certFile)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(path, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}
