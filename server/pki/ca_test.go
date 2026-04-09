package pki

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func mustGenerateRootAndIntermediate(t *testing.T) (rootCA, intCA *CA) {
	t.Helper()
	rootCertPEM, rootKeyPEM, err := GenerateCA("Test Root CA", true, nil)
	if err != nil {
		t.Fatalf("generate root CA: %v", err)
	}
	rootCA, err = ParseCA(rootCertPEM, rootKeyPEM)
	if err != nil {
		t.Fatalf("parse root CA: %v", err)
	}

	intCertPEM, intKeyPEM, err := GenerateCA("Test Intermediate CA", false, rootCA)
	if err != nil {
		t.Fatalf("generate intermediate CA: %v", err)
	}
	intCA, err = ParseCA(intCertPEM, intKeyPEM)
	if err != nil {
		t.Fatalf("parse intermediate CA: %v", err)
	}

	return rootCA, intCA
}

func generateCSR(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
}

func TestGenerateCA_Root(t *testing.T) {
	certPEM, keyPEM, err := GenerateCA("Test Root", true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ca, err := ParseCA(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !ca.Cert.IsCA {
		t.Error("expected IsCA=true")
	}
	if ca.Cert.MaxPathLen != 1 {
		t.Errorf("MaxPathLen = %d, want 1", ca.Cert.MaxPathLen)
	}
	if ca.Cert.Subject.CommonName != "Test Root" {
		t.Errorf("CN = %q, want %q", ca.Cert.Subject.CommonName, "Test Root")
	}

	// Should be valid for ~10 years
	years := ca.Cert.NotAfter.Sub(ca.Cert.NotBefore).Hours() / (24 * 365)
	if years < 9 || years > 11 {
		t.Errorf("validity = %.1f years, want ~10", years)
	}
}

func TestGenerateCA_Intermediate(t *testing.T) {
	rootCA, intCA := mustGenerateRootAndIntermediate(t)

	if !intCA.Cert.IsCA {
		t.Error("expected IsCA=true for intermediate")
	}
	if intCA.Cert.MaxPathLen != 0 || !intCA.Cert.MaxPathLenZero {
		t.Errorf("MaxPathLen = %d (zero=%v), want 0", intCA.Cert.MaxPathLen, intCA.Cert.MaxPathLenZero)
	}

	// Verify intermediate is signed by root
	err := intCA.Cert.CheckSignatureFrom(rootCA.Cert)
	if err != nil {
		t.Errorf("intermediate not signed by root: %v", err)
	}
}

func TestSignCSR(t *testing.T) {
	rootCA, intCA := mustGenerateRootAndIntermediate(t)

	csrPEM := generateCSR(t, "agent-test")
	agentID := "dev_0123456789abcdef"

	certPEM, serialHex, fingerprint, err := intCA.SignCSR(csrPEM, agentID, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("SignCSR failed: %v", err)
	}

	if serialHex == "" {
		t.Error("serial is empty")
	}
	if fingerprint == "" {
		t.Error("fingerprint is empty")
	}

	// Parse the issued cert
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block in issued cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}

	// Check agent_id in CN and SAN
	if cert.Subject.CommonName != agentID {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, agentID)
	}
	found := false
	for _, dns := range cert.DNSNames {
		if dns == agentID {
			found = true
		}
	}
	if !found {
		t.Errorf("agent_id %q not found in DNSNames: %v", agentID, cert.DNSNames)
	}

	// Check key usage
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("missing KeyUsageDigitalSignature")
	}

	// Check EKU
	hasClientAuth := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	if !hasClientAuth {
		t.Error("missing ExtKeyUsageClientAuth")
	}

	// Verify cert chain: agent cert -> intermediate -> root
	roots := x509.NewCertPool()
	roots.AddCert(rootCA.Cert)
	intermediates := x509.NewCertPool()
	intermediates.AddCert(intCA.Cert)

	_, err = cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Errorf("cert verification failed: %v", err)
	}
}

func TestSignCSR_InvalidCSR(t *testing.T) {
	_, intCA := mustGenerateRootAndIntermediate(t)

	_, _, _, err := intCA.SignCSR([]byte("not a PEM"), "dev_test", 90*24*time.Hour)
	if err == nil {
		t.Error("expected error for invalid CSR PEM")
	}
}

// generateCSRWithKey builds a CSR signed by the supplied private key, used
// to drive the rejection paths in TestSignCSR_RejectsNonP256Keys. The
// upstream helper hardcodes ECDSA P-256, which is exactly what we need to
// bypass here.
func generateCSRWithKey(t *testing.T, key interface{}) []byte {
	t.Helper()
	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent-test"},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
}

func TestSignCSR_RejectsNonP256Keys(t *testing.T) {
	_, intCA := mustGenerateRootAndIntermediate(t)

	rsa2048, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	ecP384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("p384 keygen: %v", err)
	}
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}

	cases := []struct {
		name      string
		csr       []byte
		errSubstr string
	}{
		{"rsa2048", generateCSRWithKey(t, rsa2048), "unsupported public key type"},
		{"ecdsa_p384", generateCSRWithKey(t, ecP384), "unsupported ECDSA curve"},
		{"ed25519", generateCSRWithKey(t, edPriv), "unsupported public key type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := intCA.SignCSR(tc.csr, "dev_test", 90*24*time.Hour)
			if err == nil {
				t.Fatal("expected SignCSR to reject non-P256 key, got nil error")
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSubstr)
			}
		})
	}
}

func TestCAChainPEM(t *testing.T) {
	_, intCA := mustGenerateRootAndIntermediate(t)

	chainPEM := intCA.CAChainPEM()
	block, _ := pem.Decode(chainPEM)
	if block == nil {
		t.Fatal("no PEM block in CA chain")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("block type = %q, want %q", block.Type, "CERTIFICATE")
	}
}

func TestParseCA_NotCA(t *testing.T) {
	// Generate a non-CA certificate
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{CommonName: "not-a-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	_, err := ParseCA(certPEM, keyPEM)
	if err == nil {
		t.Error("expected error for non-CA certificate")
	}
}

func mustSerial(t *testing.T) *big.Int {
	t.Helper()
	s, err := randomSerial()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
