package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

func TestExecuteFileTransfer_MissingFileID(t *testing.T) {
	e := &Executor{dropDir: t.TempDir()}
	payload, _ := json.Marshal(protocol.FileTransferPayload{})
	result := e.executeFileTransfer(context.Background(), payload)
	if result.Status != "failed" || result.Message != "file_id is required" {
		t.Errorf("got status=%s message=%s", result.Status, result.Message)
	}
}

func TestExecuteFileTransfer_NoDropDir(t *testing.T) {
	e := &Executor{dropDir: ""}
	payload, _ := json.Marshal(protocol.FileTransferPayload{FileID: "fil_test"})
	result := e.executeFileTransfer(context.Background(), payload)
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestExecuteFileTransfer_InvalidPayload(t *testing.T) {
	e := &Executor{dropDir: t.TempDir()}
	result := e.executeFileTransfer(context.Background(), []byte("not-json"))
	if result.Status != "failed" {
		t.Errorf("expected failed, got %s", result.Status)
	}
}

func TestExecuteFileTransfer_FullFlow(t *testing.T) {
	fileContent := []byte("hello world from file transfer")
	fileHash := sha256.Sum256(fileContent)
	fileHashHex := hex.EncodeToString(fileHash[:])

	dropDir := t.TempDir()

	// Mock server: download info + file data
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/files/fil_test/download":
			resp := protocol.FileDownloadResponse{
				URL:       "http://" + r.Host + "/data/fil_test",
				SizeBytes: int64(len(fileContent)),
				SHA256:    fileHashHex,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/data/fil_test":
			_, _ = w.Write(fileContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	e := &Executor{
		serverURL: srv.URL,
		client:    srv.Client(),
		dropDir:   dropDir,
	}

	payload, _ := json.Marshal(protocol.FileTransferPayload{
		FileID: "fil_test",
		Integrity: &protocol.FileTransferIntegrity{
			RequireSHA256: true,
		},
	})

	result := e.executeFileTransfer(context.Background(), payload)
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s: %s", result.Status, result.Message)
	}

	// Verify file was placed in drop dir
	finalPath := filepath.Join(dropDir, "fil_test")
	data, err := os.ReadFile(finalPath) //nolint:gosec // test file
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	if string(data) != string(fileContent) {
		t.Errorf("file content mismatch")
	}
}

func TestExecuteFileTransfer_ChecksumMismatch(t *testing.T) {
	fileContent := []byte("correct content")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/files/fil_bad/download":
			resp := protocol.FileDownloadResponse{
				URL:       "http://" + r.Host + "/data/fil_bad",
				SizeBytes: int64(len(fileContent)),
				SHA256:    "0000000000000000000000000000000000000000000000000000000000000000",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/data/fil_bad":
			_, _ = w.Write(fileContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	e := &Executor{
		serverURL: srv.URL,
		client:    srv.Client(),
		dropDir:   t.TempDir(),
	}

	payload, _ := json.Marshal(protocol.FileTransferPayload{
		FileID: "fil_bad",
		Integrity: &protocol.FileTransferIntegrity{
			RequireSHA256: true,
		},
	})

	result := e.executeFileTransfer(context.Background(), payload)
	if result.Status != "failed" {
		t.Fatalf("expected failed, got %s", result.Status)
	}
	if result.Message == "" {
		t.Error("expected error message about checksum")
	}
}

func TestShouldCheckSpace(t *testing.T) {
	if !shouldCheckSpace(nil) {
		t.Error("nil storage should default to space check enabled")
	}

	enabled := true
	if !shouldCheckSpace(&protocol.FileTransferStorage{SpaceCheckEnabled: &enabled}) {
		t.Error("explicit enabled should return true")
	}

	disabled := false
	if shouldCheckSpace(&protocol.FileTransferStorage{SpaceCheckEnabled: &disabled}) {
		t.Error("explicit disabled should return false")
	}
}

func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test")
	content := []byte("test content")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	hash, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}

	expected := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expected[:])
	if hash != expectedHex {
		t.Errorf("got %s, want %s", hash, expectedHex)
	}
}
