package models

import (
	"encoding/json"
	"time"
)

type Job struct {
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id"`
	DeviceID       string          `json:"device_id"`
	ParentJobID    string          `json:"parent_job_id,omitempty"`
	Type           string          `json:"type"`
	Status         string          `json:"status"`
	Payload        json.RawMessage `json:"payload"`
	RetryPolicy    *RetryPolicy    `json:"retry_policy,omitempty"`
	RetryCount     int             `json:"retry_count"`
	MaxRetries     int             `json:"max_retries"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedBy      string          `json:"created_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	DispatchedAt   *time.Time      `json:"dispatched_at,omitempty"`
	AcknowledgedAt *time.Time      `json:"acknowledged_at,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`

	// Populated on read for GET /v1/jobs/{id}.
	Result *JobResult `json:"result,omitempty"`
}

type RetryPolicy struct {
	MaxRetries        int `json:"max_retries"`
	RetryDelaySeconds int `json:"retry_delay_seconds,omitempty"`
}

type JobResult struct {
	ID          string     `json:"id"`
	JobID       string     `json:"job_id"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Stdout      string     `json:"stdout,omitempty"`
	Stderr      string     `json:"stderr,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Job types.
const (
	JobTypeExec           = "exec"
	JobTypePackageInstall = "package_install"
	JobTypePackageRemove  = "package_remove"
	JobTypePackageUpdate  = "package_update"
	JobTypeInventoryFull  = "inventory_full"
	JobTypeFileTransfer   = "file_transfer"
	JobTypeAgentUpdate    = "agent_update"
	JobTypeAgentRollback  = "agent_rollback"
)

// Job statuses.
const (
	JobStatusPending      = "pending"
	JobStatusQueued       = "queued"
	JobStatusCDMHold      = "cdm_hold"
	JobStatusDispatched   = "dispatched"
	JobStatusAcknowledged = "acknowledged"
	JobStatusRunning      = "running"
	JobStatusCompleted    = "completed"
	JobStatusFailed       = "failed"
	JobStatusTimedOut     = "timed_out"
	JobStatusCancelled    = "cancelled"
)

// IsTerminal returns true if the job status is a terminal state.
func IsTerminalStatus(status string) bool {
	switch status {
	case JobStatusCompleted, JobStatusFailed, JobStatusTimedOut, JobStatusCancelled:
		return true
	}
	return false
}

type ScheduledJob struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	Name        string          `json:"name"`
	JobType     string          `json:"job_type"`
	Payload     json.RawMessage `json:"payload"`
	Target      *JobTarget      `json:"target"`
	CronExpr    string          `json:"cron_expr"`
	RetryPolicy *RetryPolicy    `json:"retry_policy,omitempty"`
	Enabled     bool            `json:"enabled"`
	LastRunAt   *time.Time      `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time      `json:"next_run_at,omitempty"`
}

type JobTarget struct {
	DeviceIDs []string `json:"device_ids,omitempty"`
	GroupIDs  []string `json:"group_ids,omitempty"`
	TagIDs    []string `json:"tag_ids,omitempty"`
	SiteIDs   []string `json:"site_ids,omitempty"`
}
