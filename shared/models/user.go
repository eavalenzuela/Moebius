package models

import "time"

type User struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Email      string    `json:"email"`
	RoleID     string    `json:"role_id,omitempty"`
	SSOSubject string    `json:"sso_subject,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Role struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id,omitempty"` // empty = system role
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	IsCustom    bool     `json:"is_custom"`
}

type APIKey struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	UserID     string     `json:"user_id,omitempty"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"` // never serialized
	RoleID     string     `json:"role_id,omitempty"`
	Scope      *APIScope  `json:"scope,omitempty"`
	IsAdmin    bool       `json:"is_admin"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type APIScope struct {
	GroupIDs  []string `json:"group_ids,omitempty"`
	TagIDs    []string `json:"tag_ids,omitempty"`
	SiteIDs   []string `json:"site_ids,omitempty"`
	DeviceIDs []string `json:"device_ids,omitempty"`
}

// Predefined system role names.
const (
	RoleSuperAdmin  = "Super Admin"
	RoleTenantAdmin = "Tenant Admin"
	RoleOperator    = "Operator"
	RoleTechnician  = "Technician"
	RoleViewer      = "Viewer"
)
