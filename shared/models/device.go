package models

import "time"

type Device struct {
	ID                  string     `json:"id"`
	TenantID            string     `json:"tenant_id"`
	Hostname            string     `json:"hostname"`
	OS                  string     `json:"os"`
	OSVersion           string     `json:"os_version"`
	Arch                string     `json:"arch"`
	AgentVersion        string     `json:"agent_version"`
	Status              string     `json:"status"` // "online", "offline", "unknown", "revoked"
	LastSeenAt          *time.Time `json:"last_seen_at,omitempty"`
	RegisteredAt        time.Time  `json:"registered_at"`
	CDMEnabled          bool       `json:"cdm_enabled"`
	CDMSessionActive    bool       `json:"cdm_session_active"`
	CDMSessionExpiresAt *time.Time `json:"cdm_session_expires_at,omitempty"`
	SequenceLast        int64      `json:"sequence_last"`

	// Populated on read, not stored directly on device row.
	Groups []string `json:"groups,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Sites  []string `json:"sites,omitempty"`
}

const (
	DeviceStatusOnline  = "online"
	DeviceStatusOffline = "offline"
	DeviceStatusUnknown = "unknown"
	DeviceStatusRevoked = "revoked"
)

type Group struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
}

type Tag struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
}

type Site struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
}
