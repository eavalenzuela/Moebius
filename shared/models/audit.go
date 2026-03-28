package models

import (
	"encoding/json"
	"time"
)

type AuditEntry struct {
	ID           string          `json:"id"`
	TenantID     string          `json:"tenant_id"`
	ActorID      string          `json:"actor_id"`
	ActorType    string          `json:"actor_type"` // "user", "api_key", "agent", "system"
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	IPAddress    string          `json:"ip_address,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

const (
	ActorTypeUser   = "user"
	ActorTypeAPIKey = "api_key"
	ActorTypeAgent  = "agent"
	ActorTypeSystem = "system"
)
