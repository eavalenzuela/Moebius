package protocol

import "time"

// FileDownloadResponse is returned by GET /v1/files/{file_id}/download.
type FileDownloadResponse struct {
	URL            string    `json:"url"`
	SizeBytes      int64     `json:"size_bytes"`
	ChunkSizeBytes int       `json:"chunk_size_bytes"`
	TotalChunks    int       `json:"total_chunks"`
	SHA256         string    `json:"sha256"`
	Signature      string    `json:"signature,omitempty"`
	SignatureKeyID string    `json:"signature_key_id,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}
