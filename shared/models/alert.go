package models

import "encoding/json"

type AlertRule struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Name      string          `json:"name"`
	Condition json.RawMessage `json:"condition"`
	Channels  *AlertChannels  `json:"channels"`
	Enabled   bool            `json:"enabled"`
}

type AlertChannels struct {
	Webhooks []string `json:"webhooks,omitempty"`
	Emails   []string `json:"emails,omitempty"`
}
