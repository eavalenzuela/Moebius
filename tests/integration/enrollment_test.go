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

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// 20.1 — End-to-end enrollment test

func TestEnrollment_FullFlow(t *testing.T) {
	h := newHarness(t)

	// Create enrollment token
	token := h.createEnrollmentToken()
	if token == "" {
		t.Fatal("enrollment token is empty")
	}

	// Generate agent key + CSR
	agentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "test-host-01"},
	}, agentKey)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Enroll
	body := protocol.EnrollRequest{
		EnrollmentToken: token,
		CSR:             string(csrPEM),
		Hostname:        "test-host-01",
		OS:              "linux",
		OSVersion:       "Ubuntu 24.04",
		Arch:            "amd64",
		AgentVersion:    "1.0.0",
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	var enrollResp protocol.EnrollResponse
	readJSON(t, resp, &enrollResp)

	if enrollResp.AgentID == "" {
		t.Fatal("agent_id is empty")
	}
	if enrollResp.Certificate == "" {
		t.Fatal("certificate is empty")
	}
	if enrollResp.CAChain == "" {
		t.Fatal("ca_chain is empty")
	}
	if enrollResp.PollIntervalSeconds <= 0 {
		t.Fatalf("unexpected poll_interval: %d", enrollResp.PollIntervalSeconds)
	}

	// Verify device exists in database
	var hostname, status, os, arch string
	err = h.pool.QueryRow(context.Background(),
		`SELECT hostname, status, os, arch FROM devices WHERE id = $1 AND tenant_id = $2`,
		enrollResp.AgentID, h.tenantID,
	).Scan(&hostname, &status, &os, &arch)
	if err != nil {
		t.Fatalf("query device: %v", err)
	}
	if hostname != "test-host-01" {
		t.Errorf("hostname = %q, want %q", hostname, "test-host-01")
	}
	if status != "online" {
		t.Errorf("status = %q, want %q", status, "online")
	}
	if os != "linux" {
		t.Errorf("os = %q, want %q", os, "linux")
	}
	if arch != "amd64" {
		t.Errorf("arch = %q, want %q", arch, "amd64")
	}

	// Verify certificate stored
	var certCount int
	err = h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM agent_certificates WHERE device_id = $1`,
		enrollResp.AgentID,
	).Scan(&certCount)
	if err != nil {
		t.Fatalf("query certs: %v", err)
	}
	if certCount != 1 {
		t.Errorf("cert count = %d, want 1", certCount)
	}

	// Verify the returned certificate can be parsed
	block, _ := pem.Decode([]byte(enrollResp.Certificate))
	if block == nil {
		t.Fatal("cannot decode returned certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if cert.Subject.CommonName != enrollResp.AgentID {
		t.Errorf("cert CN = %q, want %q", cert.Subject.CommonName, enrollResp.AgentID)
	}
}

func TestEnrollment_TokenReuse(t *testing.T) {
	h := newHarness(t)

	token := h.createEnrollmentToken()

	// First enrollment with this token succeeds
	agentKey1, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER1, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "host-1"},
	}, agentKey1)
	csrPEM1 := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER1})

	body1 := protocol.EnrollRequest{
		EnrollmentToken: token,
		CSR:             string(csrPEM1),
		Hostname:        "host-1",
		OS:              "linux",
		Arch:            "amd64",
		AgentVersion:    "1.0.0",
	}
	b1, _ := json.Marshal(body1)
	resp1, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(b1))
	if err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first enrollment failed: status %d", resp1.StatusCode)
	}

	// Second enrollment with same token fails
	agentKey2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER2, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "host-2"},
	}, agentKey2)
	csrPEM2 := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER2})

	body2 := protocol.EnrollRequest{
		EnrollmentToken: token,
		CSR:             string(csrPEM2),
		Hostname:        "host-2",
		OS:              "linux",
		Arch:            "amd64",
		AgentVersion:    "1.0.0",
	}
	b2, _ := json.Marshal(body2)
	resp2, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(b2))
	if err != nil {
		t.Fatalf("second enroll: %v", err)
	}
	defer resp2.Body.Close()

	// Token was already consumed, so this should fail
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp2.StatusCode)
	}
}

func TestEnrollment_InvalidToken(t *testing.T) {
	h := newHarness(t)

	agentKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "host-bad"},
	}, agentKey)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	body := protocol.EnrollRequest{
		EnrollmentToken: "not-a-real-token",
		CSR:             string(csrPEM),
		Hostname:        "host-bad",
		OS:              "linux",
		Arch:            "amd64",
		AgentVersion:    "1.0.0",
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestEnrollment_FirstCheckin(t *testing.T) {
	h := newHarness(t)

	// Enroll agent and start mTLS server
	agentID, certPEM, keyPEM := h.enrollAgent("checkin-host")

	// Store cert in DB (enrollAgent already does this through the server)
	// Now start mTLS server and do a check-in
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status: protocol.AgentStatus{
			AgentVersion:  "1.0.0",
			UptimeSeconds: 60,
		},
		InventoryDelta: &protocol.InventoryDelta{
			Packages: &protocol.PackageDelta{
				Added: []protocol.PackageRef{
					{Name: "curl", Version: "8.5.0", Manager: "apt"},
					{Name: "vim", Version: "9.1.0", Manager: "apt"},
				},
			},
		},
	}

	resp := h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	// Verify device was updated
	var lastSeen *string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM devices WHERE id = $1`,
		agentID,
	).Scan(&lastSeen)
	if err != nil {
		t.Fatalf("query device: %v", err)
	}
	if lastSeen == nil || *lastSeen != "online" {
		t.Errorf("device status = %v, want 'online'", lastSeen)
	}

	// Verify inventory packages were stored
	var pkgCount int
	err = h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM inventory_packages WHERE device_id = $1`,
		agentID,
	).Scan(&pkgCount)
	if err != nil {
		t.Fatalf("query packages: %v", err)
	}
	if pkgCount != 2 {
		t.Errorf("package count = %d, want 2", pkgCount)
	}
}
