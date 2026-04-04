//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// 20.4 — File transfer end-to-end

func TestFileTransfer_UploadAndCreateJob(t *testing.T) {
	h := newHarness(t)

	agentID, certPEM, keyPEM := h.enrollAgent("filetx-host")

	// Prepare a test file
	fileContent := []byte("#!/bin/bash\necho 'hello from transferred file'\n")
	fileHash := sha256.Sum256(fileContent)
	fileSHA := hex.EncodeToString(fileHash[:])

	// Step 1: Initiate upload
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/files", map[string]any{
		"filename":   "test-script.sh",
		"size_bytes": len(fileContent),
		"sha256":     fileSHA,
		"mime_type":  "application/x-shellscript",
	})
	assertStatus(t, resp, http.StatusCreated)

	var uploadResp struct {
		FileID         string `json:"file_id"`
		UploadID       string `json:"upload_id"`
		ChunkSizeBytes int    `json:"chunk_size_bytes"`
		TotalChunks    int    `json:"total_chunks"`
	}
	readJSON(t, resp, &uploadResp)

	if uploadResp.FileID == "" || uploadResp.UploadID == "" {
		t.Fatal("file_id or upload_id is empty")
	}

	// Step 2: Upload chunk(s)
	req, _ := http.NewRequest("PUT",
		h.apiURL+"/v1/files/uploads/"+uploadResp.UploadID+"/chunks/0",
		bytes.NewReader(fileContent))
	req.Header.Set("Authorization", "Bearer "+h.adminKey)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload chunk: %v", err)
	}
	assertStatus(t, resp2, http.StatusOK)
	resp2.Body.Close()

	// Step 3: Complete upload
	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/files/uploads/"+uploadResp.UploadID+"/complete", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Verify file record
	var storedSHA string
	err = h.pool.QueryRow(context.Background(),
		`SELECT sha256 FROM files WHERE id = $1`, uploadResp.FileID,
	).Scan(&storedSHA)
	if err != nil {
		t.Fatalf("query file: %v", err)
	}
	if storedSHA != fileSHA {
		t.Errorf("stored sha256 = %q, want %q", storedSHA, fileSHA)
	}

	// Step 4: Create file_transfer job
	payload, _ := json.Marshal(protocol.FileTransferPayload{
		FileID: uploadResp.FileID,
		Integrity: &protocol.FileTransferIntegrity{
			RequireSHA256: true,
		},
		OnComplete: &protocol.FileTransferOnComplete{
			Exec: "bash /var/lib/moebius-agent/drop/test-script.sh",
		},
	})
	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/jobs", map[string]any{
		"type":    "file_transfer",
		"target":  map[string]any{"device_ids": []string{agentID}},
		"payload": json.RawMessage(payload),
	})
	assertStatus(t, resp, http.StatusCreated)

	var jobResp struct {
		JobIDs []string `json:"job_ids"`
	}
	readJSON(t, resp, &jobResp)

	if len(jobResp.JobIDs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobResp.JobIDs))
	}

	// Step 5: Agent checks in and receives the file_transfer job
	h.startMTLSServer()
	client := h.mtlsClient(certPEM, keyPEM)

	checkin := protocol.CheckinRequest{
		AgentID:  agentID,
		Sequence: 1,
		Status:   protocol.AgentStatus{AgentVersion: "1.0.0"},
	}
	resp = h.agentRequest(client, "POST", "/v1/agents/checkin", checkin)
	assertStatus(t, resp, http.StatusOK)

	var checkinResp protocol.CheckinResponse
	readJSON(t, resp, &checkinResp)

	if len(checkinResp.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(checkinResp.Jobs))
	}
	if checkinResp.Jobs[0].Type != "file_transfer" {
		t.Errorf("job type = %q, want %q", checkinResp.Jobs[0].Type, "file_transfer")
	}

	// Verify payload contains file_id
	var ftPayload protocol.FileTransferPayload
	if err := json.Unmarshal(checkinResp.Jobs[0].Payload, &ftPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if ftPayload.FileID != uploadResp.FileID {
		t.Errorf("payload file_id = %q, want %q", ftPayload.FileID, uploadResp.FileID)
	}
}

func TestFileTransfer_ChecksumMismatch(t *testing.T) {
	h := newHarness(t)

	fileContent := []byte("real content")
	wrongHash := hex.EncodeToString(make([]byte, 32)) // all zeros

	// Initiate upload with wrong SHA
	resp := h.apiRequestWithKey(h.adminKey, "POST", "/v1/files", map[string]any{
		"filename":   "bad-file.txt",
		"size_bytes": len(fileContent),
		"sha256":     wrongHash,
		"mime_type":  "text/plain",
	})
	assertStatus(t, resp, http.StatusCreated)

	var uploadResp struct {
		FileID   string `json:"file_id"`
		UploadID string `json:"upload_id"`
	}
	readJSON(t, resp, &uploadResp)

	// Upload chunk
	req, _ := http.NewRequest("PUT",
		h.apiURL+"/v1/files/uploads/"+uploadResp.UploadID+"/chunks/0",
		bytes.NewReader(fileContent))
	req.Header.Set("Authorization", "Bearer "+h.adminKey)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp2, _ := http.DefaultClient.Do(req)
	resp2.Body.Close()

	// Complete upload should fail due to checksum mismatch
	resp = h.apiRequestWithKey(h.adminKey, "POST", "/v1/files/uploads/"+uploadResp.UploadID+"/complete", nil)
	// Expect either 400 (bad request) or 409 (conflict) for checksum mismatch
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 400 or 409 for checksum mismatch, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
