package models

import "time"

type AgentVersion struct {
	ID         string               `json:"id"`
	Version    string               `json:"version"`
	Channel    string               `json:"channel"` // "stable", "beta", "canary"
	Changelog  string               `json:"changelog,omitempty"`
	Yanked     bool                 `json:"yanked"`
	YankReason string               `json:"yank_reason,omitempty"`
	Binaries   []AgentVersionBinary `json:"binaries,omitempty"`
	CreatedAt  time.Time            `json:"created_at"`
}

type AgentVersionBinary struct {
	ID             string `json:"id"`
	AgentVersionID string `json:"agent_version_id"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	FileID         string `json:"file_id"`
	SHA256         string `json:"sha256"`
	Signature      string `json:"signature"`
	SignatureKeyID string `json:"signature_key_id"`
}

type AgentUpdatePolicy struct {
	ID                          string `json:"id"`
	TenantID                    string `json:"tenant_id"`
	GroupID                     string `json:"group_id,omitempty"` // empty = tenant default
	Enabled                     bool   `json:"enabled"`
	Channel                     string `json:"channel"`
	Schedule                    string `json:"schedule,omitempty"` // cron expression
	RolloutStrategy             string `json:"rollout_strategy"`   // "immediate" or "gradual"
	RolloutBatchPercent         int    `json:"rollout_batch_percent"`
	RolloutBatchIntervalMinutes int    `json:"rollout_batch_interval_minutes"`
}

type Installer struct {
	ID             string    `json:"id"`
	Version        string    `json:"version"`
	Channel        string    `json:"channel"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	FileID         string    `json:"file_id"`
	SHA256         string    `json:"sha256"`
	Signature      string    `json:"signature"`
	SignatureKeyID string    `json:"signature_key_id"`
	ReleasedAt     time.Time `json:"released_at"`
	Yanked         bool      `json:"yanked"`
	YankReason     string    `json:"yank_reason,omitempty"`
}

const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
	ChannelCanary = "canary"
)
