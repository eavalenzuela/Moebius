package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
)

// CertProvider manages the agent's mTLS client certificate with hot-swap
// support. The current certificate can be atomically replaced (e.g. after
// renewal) without restarting the poller.
type CertProvider struct {
	mu   sync.RWMutex
	cert *tls.Certificate
}

// NewCertProvider loads the initial client certificate and key from disk.
func NewCertProvider(certPath, keyPath string) (*CertProvider, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	return &CertProvider{cert: &cert}, nil
}

// GetCertificate returns the current client certificate. It implements the
// function signature expected by tls.Config.GetClientCertificate.
func (cp *CertProvider) GetCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.cert, nil
}

// Swap atomically replaces the current certificate with a new one loaded
// from disk. Called after a successful renewal.
func (cp *CertProvider) Swap(certPath, keyPath string) error {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("load new cert: %w", err)
	}
	cp.mu.Lock()
	cp.cert = &cert
	cp.mu.Unlock()
	return nil
}

// LoadCAPool reads a PEM-encoded CA certificate file and returns a pool.
func LoadCAPool(caPath string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caPath) //nolint:gosec // operator-controlled path
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no valid certificates found in %s", caPath)
	}
	return pool, nil
}

// NewTLSConfig builds a tls.Config for mTLS using the given CertProvider
// and server CA pool.
func NewTLSConfig(cp *CertProvider, serverCA *x509.CertPool) *tls.Config {
	return &tls.Config{
		GetClientCertificate: cp.GetCertificate,
		RootCAs:              serverCA,
		MinVersion:           tls.VersionTLS12,
	}
}
