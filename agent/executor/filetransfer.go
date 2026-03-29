package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

func (e *Executor) executeFileTransfer(ctx context.Context, payload json.RawMessage) protocol.JobResultSubmission {
	var p protocol.FileTransferPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid file_transfer payload: " + err.Error(),
		}
	}

	if p.FileID == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "file_id is required",
		}
	}

	dropDir := e.dropDir
	if dropDir == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "drop directory not configured",
		}
	}

	// Get download metadata from server
	dlResp, err := e.getDownloadInfo(ctx, p.FileID)
	if err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "failed to get download info: " + err.Error(),
		}
	}

	// Pre-flight: check free disk space
	if shouldCheckSpace(p.Storage) {
		threshold := 0.5
		if p.Storage != nil && p.Storage.SpaceCheckThreshold != nil {
			threshold = *p.Storage.SpaceCheckThreshold
		}
		if err := checkDiskSpace(dropDir, dlResp.SizeBytes, threshold); err != nil {
			return protocol.JobResultSubmission{
				Status:  "failed",
				Message: err.Error(),
			}
		}
	}

	// Download file
	if err := os.MkdirAll(dropDir, 0o750); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "failed to create drop directory: " + err.Error(),
		}
	}

	tempFile := filepath.Join(dropDir, ".downloading-"+p.FileID)
	if err := e.downloadFile(ctx, dlResp.URL, tempFile); err != nil {
		_ = os.Remove(tempFile)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "download failed: " + err.Error(),
		}
	}

	// Verify SHA-256
	requireSHA := true
	if p.Integrity != nil {
		requireSHA = p.Integrity.RequireSHA256
	}
	if requireSHA && dlResp.SHA256 != "" {
		actualHash, err := fileSHA256(tempFile)
		if err != nil {
			_ = os.Remove(tempFile)
			return protocol.JobResultSubmission{
				Status:  "failed",
				Message: "failed to compute SHA-256: " + err.Error(),
			}
		}
		if actualHash != dlResp.SHA256 {
			_ = os.Remove(tempFile)
			return protocol.JobResultSubmission{
				Status:  "failed",
				Message: fmt.Sprintf("checksum_mismatch: expected %s, got %s", dlResp.SHA256, actualHash),
			}
		}
	}

	// Move to final location
	finalPath := filepath.Join(dropDir, filepath.Base(p.FileID))
	if err := os.Rename(tempFile, finalPath); err != nil {
		_ = os.Remove(tempFile)
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "failed to move file to drop directory: " + err.Error(),
		}
	}

	// Execute on_complete command if specified
	if p.OnComplete != nil && p.OnComplete.Exec != "" {
		out, err := runOnComplete(ctx, p.OnComplete.Exec, finalPath)
		if err != nil {
			return protocol.JobResultSubmission{
				Status:  "failed",
				Message: "on_complete failed: " + err.Error(),
				Stdout:  out,
			}
		}
	}

	return protocol.JobResultSubmission{
		Status:  "completed",
		Message: "file transferred to " + finalPath,
	}
}

func (e *Executor) getDownloadInfo(ctx context.Context, fileID string) (*protocol.FileDownloadResponse, error) {
	url := e.serverURL + "/v1/agents/files/" + fileID + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET download info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var dlResp protocol.FileDownloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&dlResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &dlResp, nil
}

func (e *Executor) downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	f, err := os.Create(destPath) //nolint:gosec // server-controlled path
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // server-controlled path
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func shouldCheckSpace(storage *protocol.FileTransferStorage) bool {
	if storage != nil && storage.SpaceCheckEnabled != nil {
		return *storage.SpaceCheckEnabled
	}
	return true // default enabled
}

func runOnComplete(ctx context.Context, command, filePath string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command) //nolint:gosec // server-dispatched
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // server-dispatched
	}
	cmd.Env = append(os.Environ(), "MOEBIUS_FILE_PATH="+filePath)

	out, err := cmd.CombinedOutput()
	return string(out), err
}
