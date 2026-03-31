package models

import "time"

// Rollout tracks the gradual rollout state for an agent version.
type Rollout struct {
	ID                   string     `json:"id"`
	AgentVersionID       string     `json:"agent_version_id"`
	TenantID             string     `json:"tenant_id"`
	Status               string     `json:"status"` // "in_progress", "paused", "completed", "aborted"
	Strategy             string     `json:"strategy"`
	BatchPercent         int        `json:"batch_percent"`
	BatchIntervalMinutes int        `json:"batch_interval_minutes"`
	CurrentBatch         int        `json:"current_batch"`
	TotalDevices         int        `json:"total_devices"`
	UpdatedDevices       int        `json:"updated_devices"`
	RolledBackDevices    int        `json:"rolled_back_devices"`
	Seed                 int64      `json:"seed"` // deterministic random seed
	LastBatchAt          *time.Time `json:"last_batch_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
}

const (
	RolloutStatusInProgress = "in_progress"
	RolloutStatusPaused     = "paused"
	RolloutStatusCompleted  = "completed"
	RolloutStatusAborted    = "aborted"
)
