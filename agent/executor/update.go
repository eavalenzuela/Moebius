package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/eavalenzuela/Moebius/agent/update"
	"github.com/eavalenzuela/Moebius/shared/protocol"
	"github.com/eavalenzuela/Moebius/shared/version"
)

func (e *Executor) executeAgentUpdate(ctx context.Context, jobID string, payload json.RawMessage) protocol.JobResultSubmission {
	var p protocol.AgentUpdatePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid agent_update payload: " + err.Error(),
		}
	}

	// 1. Pre-flight checks
	if p.Version == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "version is required in update payload",
		}
	}

	if !p.Force && version.Version != "dev" && p.Version <= version.Version {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: fmt.Sprintf("target version %s is not newer than current %s (use force to downgrade)", p.Version, version.Version),
		}
	}

	if e.platform == nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "platform not configured (cannot determine binary paths)",
		}
	}

	binaryPath := e.platform.BinaryPath()
	stagingPath := e.platform.BinaryStagingPath()
	previousPath := e.platform.BinaryPreviousPath()
	pendingPath := e.platform.PendingUpdatePath()

	// Verify current binary is accessible
	if _, err := os.Stat(binaryPath); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "current binary not found: " + err.Error(),
		}
	}

	// Disk space check
	if p.SizeBytes > 0 {
		if err := checkDiskSpace(e.platform.BinaryDir(), p.SizeBytes, 0.5); err != nil {
			return protocol.JobResultSubmission{
				Status:  "failed",
				Message: err.Error(),
			}
		}
	}

	// 2. Download new binary to staging path
	if p.DownloadURL == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "download_url is required",
		}
	}

	if err := e.downloadFile(ctx, p.DownloadURL, stagingPath); err != nil {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "download failed: " + err.Error(),
		}
	}

	// 3. Verify SHA-256
	if p.SHA256 == "" {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "sha256 is required for agent updates",
		}
	}

	actualHash, err := fileSHA256(stagingPath)
	if err != nil {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "sha256 computation failed: " + err.Error(),
		}
	}
	if actualHash != p.SHA256 {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: fmt.Sprintf("checksum_mismatch: expected %s, got %s", p.SHA256, actualHash),
		}
	}

	// Verify Ed25519 signature
	if p.Signature == "" || p.SignatureKeyID == "" {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "signature and signature_key_id are required for agent updates",
		}
	}

	if err := e.verifySignature(ctx, stagingPath, p.Signature, p.SignatureKeyID); err != nil {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "signature verification failed: " + err.Error(),
		}
	}

	// Make staging binary executable
	if err := os.Chmod(stagingPath, 0o755); err != nil { //nolint:gosec // agent binary needs to be executable
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "failed to set binary permissions: " + err.Error(),
		}
	}

	// 4. Side-by-side staging
	// Copy current → .previous
	if err := copyFile(binaryPath, previousPath); err != nil {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "failed to backup current binary: " + err.Error(),
		}
	}

	// Atomic rename: staging → current
	if err := os.Rename(stagingPath, binaryPath); err != nil {
		_ = os.Remove(stagingPath)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "failed to replace binary: " + err.Error(),
		}
	}

	// 5. Write pending_update.json
	pollInterval := 30 // default poll interval
	if e.pollInterval > 0 {
		pollInterval = e.pollInterval
	}
	pending := &update.PendingUpdate{
		JobID:           jobID,
		ExpectedVersion: p.Version,
		PreviousVersion: version.Version,
		Deadline:        time.Now().Add(time.Duration(3*pollInterval) * time.Second),
	}
	if err := update.WritePending(pendingPath, pending); err != nil {
		// Non-fatal but concerning — we log and continue
		e.log.Error("failed to write pending update file", "error", err.Error())
	}

	// 6. Submit partial "restarting" result
	// This is returned to runJob which submits it, then the service manager
	// will restart us.
	return protocol.JobResultSubmission{
		Status:  "restarting",
		Message: fmt.Sprintf("New binary installed, restarting into v%s", p.Version),
	}
}

// verifySignature fetches the public key from the server and verifies the Ed25519 signature.
func (e *Executor) verifySignature(ctx context.Context, filePath, signatureB64, keyID string) error {
	// Fetch the signing key from server
	pubKey, err := e.fetchSigningKey(ctx, keyID)
	if err != nil {
		return fmt.Errorf("fetch signing key: %w", err)
	}

	// Read file contents for signature verification
	data, err := os.ReadFile(filePath) //nolint:gosec // controlled path
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Compute SHA-256 of file (signature is over the hash)
	hash := sha256.Sum256(data)

	// Decode signature
	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	// Verify
	if !ed25519.Verify(pubKey, hash[:], sig) {
		return fmt.Errorf("Ed25519 signature is invalid")
	}

	return nil
}

// fetchSigningKey retrieves the Ed25519 public key for the given key ID.
func (e *Executor) fetchSigningKey(ctx context.Context, keyID string) (ed25519.PublicKey, error) {
	url := e.serverURL + "/v1/agents/signing-keys/" + keyID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var keyResp struct {
		PublicKey string `json:"public_key"` // PEM-encoded
	}
	if err := json.NewDecoder(resp.Body).Decode(&keyResp); err != nil {
		return nil, fmt.Errorf("decode key response: %w", err)
	}

	// Parse PEM
	block, _ := pem.Decode([]byte(keyResp.PublicKey))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM data")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	edKey, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not Ed25519")
	}

	return edKey, nil
}

// copyFile copies src to dst, overwriting dst if it exists.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // controlled path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst) //nolint:gosec // controlled path
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	// Preserve permissions
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}
