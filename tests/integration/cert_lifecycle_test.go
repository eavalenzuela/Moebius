//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// 20.6 — Certificate lifecycle

func TestCertLifecycle_Renewal(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("renew-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Generate new key + CSR for renewal
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: agentID},
	}, newKey)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Request renewal
	renewReq := protocol.RenewRequest{CSR: string(csrPEM)}
	resp := h.agentRequest(client, "POST", "/v1/agents/renew", renewReq)
	assertStatus(t, resp, http.StatusOK)

	var renewResp protocol.RenewResponse
	readJSON(t, resp, &renewResp)

	if renewResp.Certificate == "" {
		t.Fatal("renewed certificate is empty")
	}
	if renewResp.CAChain == "" {
		t.Fatal("CA chain is empty")
	}

	// Parse new certificate
	block, _ := pem.Decode([]byte(renewResp.Certificate))
	if block == nil {
		t.Fatal("cannot decode renewed certificate")
	}
	newCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if newCert.Subject.CommonName != agentID {
		t.Errorf("cert CN = %q, want %q", newCert.Subject.CommonName, agentID)
	}
	// Cert should be valid for roughly 90 days
	validity := newCert.NotAfter.Sub(newCert.NotBefore)
	if validity < 89*24*time.Hour || validity > 91*24*time.Hour {
		t.Errorf("cert validity = %v, expected ~90 days", validity)
	}

	// Verify two certificates now exist in DB
	var certCount int
	err = h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_certificates WHERE device_id = $1`, agentID,
	).Scan(&certCount)
	if err != nil {
		t.Fatalf("query certs: %v", err)
	}
	if certCount != 2 {
		t.Errorf("cert count = %d, want 2 (original + renewed)", certCount)
	}
}

func TestCertLifecycle_RevokedCertRejected(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("revoked-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Revoke the certificate
	_, err := h.pool.Exec(context.Background(),
		`UPDATE agent_certificates SET revoked_at = $1, revocation_reason = 'test revocation'
		 WHERE device_id = $2`, time.Now().UTC(), agentID)
	if err != nil {
		t.Fatalf("revoke cert: %v", err)
	}

	// Try to check in — should be rejected
	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp := h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)

	// mTLS middleware checks revocation and returns 401
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for revoked cert, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCertLifecycle_RevokedDeviceRejected(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("device-revoked-host")
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	// Revoke the device (not the cert)
	_, err := h.pool.Exec(context.Background(),
		`UPDATE devices SET status = 'revoked' WHERE id = $1`, agentID)
	if err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	// Try to check in — should be rejected
	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp := h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for revoked device, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCertLifecycle_ExpiredCertRejected(t *testing.T) {
	h := newHarness(t)

	// Enroll normally
	agentID, _, keyPEM := h.enrollAgent("expired-host")

	// Manually create an expired certificate signed by our CA
	agentKeyBlock, _ := pem.Decode(keyPEM)
	agentKey, _ := x509.ParseECPrivateKey(agentKeyBlock.Bytes)

	serial, _ := randomSerial()
	expired := time.Now().Add(-48 * time.Hour) // 2 days ago
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: agentID},
		DNSNames:     []string{agentID},
		NotBefore:    expired.Add(-24 * time.Hour),
		NotAfter:     expired, // expired yesterday
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, h.ca.Cert, &agentKey.PublicKey, h.ca.Key)
	if err != nil {
		t.Fatalf("create expired cert: %v", err)
	}
	expiredCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	h.startMTLSServer()
	client := h.mtlsClient(expiredCertPEM, keyPEM)

	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	b, _ := json.Marshal(checkin)
	req, err := http.NewRequest("POST", h.apiURL+"/v1/agents/checkin", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// TLS handshake rejection is the expected outcome for an expired cert
		t.Logf("request correctly rejected: %v", err)
		return
	}
	defer resp.Body.Close()

	// If we somehow got a response, it shouldn't be 200
	if resp.StatusCode == http.StatusOK {
		t.Error("expected non-200 for expired cert, got 200")
	}
}
