package protocol

import "time"

// CheckinRequest is sent by the agent on every poll (POST /v1/agents/checkin).
type CheckinRequest struct {
	AgentID        string          `json:"agent_id"`
	Timestamp      time.Time       `json:"timestamp"`
	Sequence       int64           `json:"sequence"`
	Status         AgentStatus     `json:"status"`
	InventoryDelta *InventoryDelta `json:"inventory_delta,omitempty"`
}

type AgentStatus struct {
	UptimeSeconds       int64      `json:"uptime_seconds"`
	CDMEnabled          bool       `json:"cdm_enabled"`
	CDMSessionActive    bool       `json:"cdm_session_active"`
	CDMSessionExpiresAt *time.Time `json:"cdm_session_expires_at,omitempty"`
	AgentVersion        string     `json:"agent_version"`

	// Set after a failed update to inform the server.
	LastUpdateFailed bool   `json:"last_update_failed,omitempty"`
	LastUpdateJobID  string `json:"last_update_job_id,omitempty"`
	LastUpdateError  string `json:"last_update_error,omitempty"`
}

type InventoryDelta struct {
	Packages *PackageDelta `json:"packages,omitempty"`
}

type PackageDelta struct {
	Added   []PackageRef `json:"added,omitempty"`
	Removed []PackageRef `json:"removed,omitempty"`
	Updated []PackageRef `json:"updated,omitempty"`
}

type PackageRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Manager string `json:"manager"`
}

// CheckinResponse is returned by the server on every poll.
type CheckinResponse struct {
	Timestamp time.Time     `json:"timestamp"`
	Jobs      []JobDispatch `json:"jobs"`
	Config    *AgentConfig  `json:"config,omitempty"`
}

type AgentConfig struct {
	PollIntervalSeconds int `json:"poll_interval_seconds,omitempty"`
}
