package renewal

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// RenewalThreshold is the duration before cert expiry at which renewal begins.
const RenewalThreshold = 30 * 24 * time.Hour // 30 days

// backoff schedule for failed renewal attempts.
var retryBackoff = []time.Duration{
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
}

// Result holds the data from a successful certificate renewal.
type Result struct {
	CertPEM    string
	KeyPEM     string
	CAChainPEM string
}

// NeedsRenewal reads the client certificate from disk and returns true if
// it expires within RenewalThreshold.
func NeedsRenewal(certPath string) (bool, time.Time, error) {
	certPEM, err := os.ReadFile(certPath) //nolint:gosec // operator-controlled path
	if err != nil {
		return false, time.Time{}, fmt.Errorf("read cert: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, time.Time{}, fmt.Errorf("no PEM block found in %s", certPath)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("parse cert: %w", err)
	}

	return time.Until(cert.NotAfter) <= RenewalThreshold, cert.NotAfter, nil
}

// Renew generates a new keypair and CSR, posts to the server's renewal
// endpoint using the current mTLS client, and returns the new credentials.
func Renew(serverURL string, client *http.Client, log *slog.Logger) (*Result, error) {
	// Generate new keypair
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	hostname, _ := os.Hostname()
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: hostname},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	reqBody := protocol.RenewRequest{CSR: string(csrPEM)}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal renew request: %w", err)
	}

	url := strings.TrimRight(serverURL, "/") + "/v1/agents/renew"
	log.Info("requesting certificate renewal", slog.String("url", url))

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST renew: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read renew response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("renewal failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var renewResp protocol.RenewResponse
	if err := json.Unmarshal(respBody, &renewResp); err != nil {
		return nil, fmt.Errorf("parse renew response: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &Result{
		CertPEM:    renewResp.Certificate,
		KeyPEM:     string(keyPEM),
		CAChainPEM: renewResp.CAChain,
	}, nil
}

// SaveRenewal writes the renewed certificate and key to disk.
func SaveRenewal(r *Result, certPath, keyPath, caPath string) error {
	files := []struct {
		path string
		data string
		mode os.FileMode
	}{
		{certPath, r.CertPEM, 0o640},
		{keyPath, r.KeyPEM, 0o600},
		{caPath, r.CAChainPEM, 0o644},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.data), f.mode); err != nil { //nolint:gosec // permission modes are intentionally varied
			return fmt.Errorf("write %s: %w", f.path, err)
		}
	}
	return nil
}

// RetryBackoff returns the backoff duration for the given attempt number
// (0-indexed). Returns the last duration for attempts beyond the schedule.
func RetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(retryBackoff) {
		return retryBackoff[len(retryBackoff)-1]
	}
	return retryBackoff[attempt]
}
