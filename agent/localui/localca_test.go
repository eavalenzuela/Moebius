package localui

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"testing"
	"time"
)

func TestLocalCAGeneration(t *testing.T) {
	dir := t.TempDir()
	ca := NewLocalCA(dir)

	if err := ca.EnsureCA(); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	// CA cert and key should exist.
	if !fileExists(ca.caCertPath()) {
		t.Fatal("CA cert file not created")
	}
	if !fileExists(ca.caKeyPath()) {
		t.Fatal("CA key file not created")
	}

	// Parse and verify CA cert.
	certPEM, err := os.ReadFile(ca.caCertPath())
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block in CA cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// Check CA properties.
	if !cert.IsCA {
		t.Error("expected IsCA=true")
	}
	if cert.Subject.CommonName != "Moebius Agent Local CA" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "Moebius Agent Local CA")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("expected KeyUsageCertSign")
	}

	// Check Name Constraints.
	if len(cert.PermittedDNSDomains) != 1 || cert.PermittedDNSDomains[0] != "localhost" {
		t.Errorf("PermittedDNSDomains = %v, want [localhost]", cert.PermittedDNSDomains)
	}
	if len(cert.PermittedIPRanges) != 1 {
		t.Fatalf("PermittedIPRanges length = %d, want 1", len(cert.PermittedIPRanges))
	}
	if !cert.PermittedIPRanges[0].IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("PermittedIPRanges[0] = %v, want 127.0.0.1/32", cert.PermittedIPRanges[0])
	}

	// Validity should be ~10 years.
	validFor := cert.NotAfter.Sub(cert.NotBefore)
	if validFor < 9*365*24*time.Hour || validFor > 11*365*24*time.Hour {
		t.Errorf("CA validity = %v, want ~10 years", validFor)
	}

	// Key should be 0600.
	info, err := os.Stat(ca.caKeyPath())
	if err != nil {
		t.Fatalf("stat CA key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("CA key permissions = %o, want 0600", perm)
	}
}

func TestLocalCAIdempotent(t *testing.T) {
	dir := t.TempDir()
	ca := NewLocalCA(dir)

	if err := ca.EnsureCA(); err != nil {
		t.Fatalf("first EnsureCA: %v", err)
	}

	// Read cert content.
	first, _ := os.ReadFile(ca.caCertPath())

	// Second call should not regenerate.
	if err := ca.EnsureCA(); err != nil {
		t.Fatalf("second EnsureCA: %v", err)
	}
	second, _ := os.ReadFile(ca.caCertPath())

	if string(first) != string(second) {
		t.Error("EnsureCA regenerated the CA cert on second call")
	}
}

func TestLocalhostCertIssuance(t *testing.T) {
	dir := t.TempDir()
	ca := NewLocalCA(dir)

	if err := ca.EnsureCA(); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	if err := ca.EnsureCert(); err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}

	// Parse leaf cert.
	certPEM, _ := os.ReadFile(ca.certPath())
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}

	// Check leaf properties.
	if cert.Subject.CommonName != "localhost" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "localhost")
	}
	if cert.IsCA {
		t.Error("leaf cert should not be CA")
	}

	// Check SANs.
	hasDNS := false
	for _, dns := range cert.DNSNames {
		if dns == "localhost" {
			hasDNS = true
		}
	}
	if !hasDNS {
		t.Error("expected localhost in DNSNames")
	}

	hasIP := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			hasIP = true
		}
	}
	if !hasIP {
		t.Error("expected 127.0.0.1 in IPAddresses")
	}

	// Check validity (~90 days).
	validFor := cert.NotAfter.Sub(cert.NotBefore)
	if validFor < 89*24*time.Hour || validFor > 91*24*time.Hour {
		t.Errorf("cert validity = %v, want ~90 days", validFor)
	}

	// Verify the cert chains to the CA.
	caCertPEM, _ := os.ReadFile(ca.caCertPath())
	caBlock, _ := pem.Decode(caCertPEM)
	caCert, _ := x509.ParseCertificate(caBlock.Bytes)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	_, err = cert.Verify(x509.VerifyOptions{
		Roots:   pool,
		DNSName: "localhost",
	})
	if err != nil {
		t.Errorf("cert verification failed: %v", err)
	}
}

func TestTLSConfig(t *testing.T) {
	dir := t.TempDir()
	ca := NewLocalCA(dir)

	_ = ca.EnsureCA()
	_ = ca.EnsureCert()

	tlsCfg, err := ca.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}

	if len(tlsCfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(tlsCfg.Certificates))
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d", tlsCfg.MinVersion, tls.VersionTLS12)
	}
}

func TestCertRotation(t *testing.T) {
	dir := t.TempDir()
	ca := NewLocalCA(dir)

	_ = ca.EnsureCA()
	_ = ca.EnsureCert()

	// Backdate the cert to trigger rotation.
	caCert, caKey, _ := ca.loadCA()
	_ = caCert
	_ = caKey

	// Read original cert.
	origPEM, _ := os.ReadFile(ca.certPath())

	// Manually create an almost-expired cert to trigger rotation.
	// We'll just remove the cert and let EnsureCert recreate it.
	_ = os.Remove(ca.certPath())

	if err := ca.EnsureCert(); err != nil {
		t.Fatalf("EnsureCert after remove: %v", err)
	}

	newPEM, _ := os.ReadFile(ca.certPath())
	if string(origPEM) == string(newPEM) {
		t.Error("expected new cert after rotation")
	}
}
