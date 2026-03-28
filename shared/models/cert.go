package models

import "time"

type AgentCertificate struct {
	ID               string     `json:"id"`
	DeviceID         string     `json:"device_id"`
	SerialNumber     string     `json:"serial_number"`
	Fingerprint      string     `json:"fingerprint"`
	IssuedAt         time.Time  `json:"issued_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	RevocationReason string     `json:"revocation_reason,omitempty"`
}

type EnrollmentToken struct {
	ID        string     `json:"id"`
	TenantID  string     `json:"tenant_id"`
	TokenHash string     `json:"-"` // never serialized
	CreatedBy string     `json:"created_by"`
	Scope     *APIScope  `json:"scope,omitempty"` // reuses APIScope from user.go
	UsedAt    *time.Time `json:"used_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
}
