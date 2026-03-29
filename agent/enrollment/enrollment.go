package enrollment

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
	"runtime"
	"strings"

	"github.com/moebius-oss/moebius/shared/protocol"
	"github.com/moebius-oss/moebius/shared/version"
)

// Result holds the data returned by a successful enrollment.
type Result struct {
	AgentID             string
	CertPEM             string
	KeyPEM              string
	CAChainPEM          string
	PollIntervalSeconds int
}

// Enroll performs the agent enrollment flow:
//  1. Read enrollment token from disk
//  2. Generate ECDSA P-256 keypair
//  3. Build CSR with hostname and OS metadata
//  4. POST /v1/agents/enroll
//  5. Return cert, key, CA chain, and agent_id
func Enroll(serverURL, tokenPath string, client *http.Client, log *slog.Logger) (*Result, error) {
	// 1. Read enrollment token
	tokenBytes, err := os.ReadFile(tokenPath) //nolint:gosec // operator-controlled path
	if err != nil {
		return nil, fmt.Errorf("read enrollment token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("enrollment token is empty")
	}

	// 2. Generate keypair
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// 3. Build CSR
	hostname, _ := os.Hostname()
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: hostname},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// 4. POST enrollment request
	osName, osVersion := detectOS()
	reqBody := protocol.EnrollRequest{
		EnrollmentToken: token,
		CSR:             string(csrPEM),
		Hostname:        hostname,
		OS:              osName,
		OSVersion:       osVersion,
		Arch:            runtime.GOARCH,
		AgentVersion:    version.Version,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal enroll request: %w", err)
	}

	url := strings.TrimRight(serverURL, "/") + "/v1/agents/enroll"
	log.Info("enrolling with server", slog.String("url", url))

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST enroll: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read enroll response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrollment failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var enrollResp protocol.EnrollResponse
	if err := json.Unmarshal(respBody, &enrollResp); err != nil {
		return nil, fmt.Errorf("parse enroll response: %w", err)
	}

	// 5. Encode private key
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &Result{
		AgentID:             enrollResp.AgentID,
		CertPEM:             enrollResp.Certificate,
		KeyPEM:              string(keyPEM),
		CAChainPEM:          enrollResp.CAChain,
		PollIntervalSeconds: enrollResp.PollIntervalSeconds,
	}, nil
}

// SaveCredentials writes the enrollment result to disk and removes the
// enrollment token.
func SaveCredentials(r *Result, certPath, keyPath, caPath, agentIDPath, tokenPath string) error {
	files := []struct {
		path string
		data string
		mode os.FileMode
	}{
		{certPath, r.CertPEM, 0o640},
		{keyPath, r.KeyPEM, 0o600},
		{caPath, r.CAChainPEM, 0o644},
		{agentIDPath, r.AgentID, 0o640},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.data), f.mode); err != nil { //nolint:gosec // permission modes are intentionally varied
			return fmt.Errorf("write %s: %w", f.path, err)
		}
	}

	// Delete the enrollment token — it's single-use
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove enrollment token: %w", err)
	}

	return nil
}

// detectOS returns (os, os_version) for the enrollment request.
func detectOS() (osName, osVersion string) {
	switch runtime.GOOS {
	case "linux":
		return "linux", readOSRelease()
	case "windows":
		return "windows", "" // populated at build time or via syscall in later phase
	default:
		return runtime.GOOS, ""
	}
}

// readOSRelease tries to extract PRETTY_NAME from /etc/os-release.
func readOSRelease() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(val, `"`)
		}
	}
	return ""
}
