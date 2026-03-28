package protocol

import (
	"encoding/json"
	"time"
)

// JobDispatch is a single job included in a CheckinResponse.
type JobDispatch struct {
	JobID     string          `json:"job_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// JobResultSubmission is sent by the agent after executing a job
// (POST /v1/agents/jobs/{job_id}/result).
type JobResultSubmission struct {
	Status      string     `json:"status"` // "completed", "failed", "timed_out", "restarting"
	ExitCode    *int       `json:"exit_code,omitempty"`
	Stdout      string     `json:"stdout,omitempty"`
	Stderr      string     `json:"stderr,omitempty"`
	Message     string     `json:"message,omitempty"` // human-readable context (e.g. rollback reason)
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ─── Job payloads (per job type) ────────────────────────

type ExecPayload struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type PackageInstallPayload struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Manager string `json:"manager,omitempty"` // if empty, agent auto-detects
}

type PackageRemovePayload struct {
	Name    string `json:"name"`
	Manager string `json:"manager,omitempty"`
}

type PackageUpdatePayload struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Manager string `json:"manager,omitempty"`
}

type InventoryFullPayload struct {
	// No fields — the agent collects everything.
}

type FileTransferPayload struct {
	FileID     string                  `json:"file_id"`
	Integrity  *FileTransferIntegrity  `json:"integrity,omitempty"`
	Storage    *FileTransferStorage    `json:"storage,omitempty"`
	OnComplete *FileTransferOnComplete `json:"on_complete,omitempty"`
}

type FileTransferIntegrity struct {
	RequireSHA256    bool `json:"require_sha256"`
	RequireSignature bool `json:"require_signature"`
}

type FileTransferStorage struct {
	SpaceCheckEnabled   *bool    `json:"space_check_enabled,omitempty"`
	SpaceCheckThreshold *float64 `json:"space_check_threshold,omitempty"`
}

type FileTransferOnComplete struct {
	Exec string `json:"exec,omitempty"`
}

type AgentUpdatePayload struct {
	Version            string `json:"version"`
	Channel            string `json:"channel,omitempty"`
	DownloadURL        string `json:"download_url"`
	SHA256             string `json:"sha256"`
	Signature          string `json:"signature"`
	SignatureKeyID     string `json:"signature_key_id"`
	SizeBytes          int64  `json:"size_bytes"`
	MinRollbackVersion string `json:"min_rollback_version,omitempty"`
	Force              bool   `json:"force,omitempty"` // allow downgrade
}
