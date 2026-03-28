package models

import "time"

type SigningKey struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Algorithm   string    `json:"algorithm"` // "ed25519"
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type File struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id"`
	Filename          string    `json:"filename"`
	SizeBytes         int64     `json:"size_bytes"`
	SHA256            string    `json:"sha256"`
	Signature         string    `json:"signature,omitempty"`
	SignatureKeyID    string    `json:"signature_key_id,omitempty"`
	SignatureVerified bool      `json:"signature_verified"`
	MIMEType          string    `json:"mime_type,omitempty"`
	StorageBackend    string    `json:"storage_backend"` // "server" or "s3"
	StoragePath       string    `json:"storage_path"`
	CreatedBy         string    `json:"created_by,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type FileUpload struct {
	ID             string     `json:"id"`
	FileID         string     `json:"file_id"`
	TenantID       string     `json:"tenant_id"`
	ChunkSizeBytes int        `json:"chunk_size_bytes"`
	TotalChunks    int        `json:"total_chunks"`
	UploadedChunks []int      `json:"uploaded_chunks"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
}
