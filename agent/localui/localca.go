// Package localui implements the agent's localhost-only HTTPS web UI.
package localui

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour // ~10 years
	certValidity = 90 * 24 * time.Hour       // 90 days
	renewBefore  = 30 * 24 * time.Hour       // renew 30 days before expiry
)

// LocalCA manages the per-device CA used to sign localhost TLS certificates.
type LocalCA struct {
	dataDir string // directory for CA and cert files
}

// NewLocalCA returns a LocalCA that stores keys in dataDir.
func NewLocalCA(dataDir string) *LocalCA {
	return &LocalCA{dataDir: dataDir}
}

func (ca *LocalCA) caCertPath() string { return filepath.Join(ca.dataDir, "local-ca.crt") }
func (ca *LocalCA) caKeyPath() string  { return filepath.Join(ca.dataDir, "local-ca.key") }
func (ca *LocalCA) certPath() string   { return filepath.Join(ca.dataDir, "local-tls.crt") }
func (ca *LocalCA) keyPath() string    { return filepath.Join(ca.dataDir, "local-tls.key") }

// CACertPath returns the path to the CA certificate (for trust store installation).
func (ca *LocalCA) CACertPath() string { return ca.caCertPath() }

// EnsureCA generates a CA keypair if one doesn't exist. Returns the CA cert PEM path.
func (ca *LocalCA) EnsureCA() error {
	if fileExists(ca.caCertPath()) && fileExists(ca.caKeyPath()) {
		return nil
	}
	return ca.generateCA()
}

// EnsureCert generates or rotates the localhost TLS certificate.
// Must be called after EnsureCA.
func (ca *LocalCA) EnsureCert() error {
	if fileExists(ca.certPath()) && fileExists(ca.keyPath()) {
		// Check if rotation is needed.
		needs, err := ca.needsRotation()
		if err != nil {
			return fmt.Errorf("check rotation: %w", err)
		}
		if !needs {
			return nil
		}
	}
	return ca.issueCert()
}

// TLSConfig returns a tls.Config using the localhost cert, or an error if
// the cert doesn't exist yet.
func (ca *LocalCA) TLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(ca.certPath(), ca.keyPath())
	if err != nil {
		return nil, fmt.Errorf("load localhost cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func (ca *LocalCA) generateCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Moebius Agent Local CA",
			Organization: []string{"Moebius Agent"},
		},
		NotBefore:             now,
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,

		// Name Constraints: this CA can only sign certs for localhost / 127.0.0.1.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"localhost"},
		PermittedIPRanges: []*net.IPNet{
			{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(32, 32)},
		},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}

	if err := writePEM(ca.caCertPath(), "CERTIFICATE", certDER, 0o644); err != nil {
		return err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}

	return writePEM(ca.caKeyPath(), "EC PRIVATE KEY", keyDER, 0o600)
}

func (ca *LocalCA) issueCert() error {
	// Load CA.
	caCert, caKey, err := ca.loadCA()
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}

	// Generate leaf key.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"Moebius Agent"},
		},
		NotBefore: now,
		NotAfter:  now.Add(certValidity),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create leaf cert: %w", err)
	}

	if err := writePEM(ca.certPath(), "CERTIFICATE", certDER, 0o644); err != nil {
		return err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal leaf key: %w", err)
	}

	return writePEM(ca.keyPath(), "EC PRIVATE KEY", keyDER, 0o600)
}

func (ca *LocalCA) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(ca.caCertPath()) //nolint:gosec // agent-controlled path
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("no PEM block in CA cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}

	keyPEM, err := os.ReadFile(ca.caKeyPath()) //nolint:gosec // agent-controlled path
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("no PEM block in CA key")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA key: %w", err)
	}

	return cert, key, nil
}

func (ca *LocalCA) needsRotation() (bool, error) {
	certPEM, err := os.ReadFile(ca.certPath()) //nolint:gosec // agent-controlled path
	if err != nil {
		return true, nil
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return true, nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true, nil
	}
	return time.Now().UTC().Add(renewBefore).After(cert.NotAfter), nil
}

func writePEM(path, blockType string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec // agent-controlled path
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		return fmt.Errorf("encode PEM %s: %w", path, err)
	}
	return nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
