package models

import "time"

type Tenant struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Slug      string        `json:"slug"`
	Config    *TenantConfig `json:"config,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type TenantConfig struct {
	DefaultPollIntervalSeconds int            `json:"default_poll_interval_seconds,omitempty"`
	DefaultCertLifetimeDays    int            `json:"default_cert_lifetime_days,omitempty"`
	SSO                        *SSOConfig     `json:"sso,omitempty"`
	Storage                    *StorageConfig `json:"storage,omitempty"`
	Quotas                     *TenantQuotas  `json:"quotas,omitempty"`
}

// TenantQuotas overrides the global per-tenant resource ceilings for
// a single tenant. A zero value means "inherit the global default";
// -1 means "unlimited".
type TenantQuotas struct {
	MaxDevices       int64 `json:"max_devices,omitempty"`
	MaxQueuedJobs    int64 `json:"max_queued_jobs,omitempty"`
	MaxAPIKeys       int64 `json:"max_api_keys,omitempty"`
	MaxFileSizeBytes int64 `json:"max_file_size_bytes,omitempty"`
}

type SSOConfig struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	IssuerURL string `json:"issuer_url,omitempty"`
}

type StorageConfig struct {
	Backend              string `json:"backend,omitempty"` // "server" or "s3"
	Endpoint             string `json:"endpoint,omitempty"`
	Bucket               string `json:"bucket,omitempty"`
	Region               string `json:"region,omitempty"`
	PresignExpirySeconds int    `json:"presign_expiry_seconds,omitempty"`
}
