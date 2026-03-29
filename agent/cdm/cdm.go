// Package cdm implements Customer Device Mode state management.
// CDM state is local-authoritative: it lives on the agent and is
// reported to the server via check-in. The server never sets CDM state.
package cdm

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// State represents the persisted CDM state.
type State struct {
	Enabled          bool       `json:"enabled"`
	SessionActive    bool       `json:"session_active"`
	SessionExpiresAt *time.Time `json:"session_expires_at,omitempty"`
	SessionGrantedBy string     `json:"session_granted_by,omitempty"`
	SessionGrantedAt *time.Time `json:"session_granted_at,omitempty"`
}

// Manager manages CDM state with persistence and expiry.
type Manager struct {
	mu       sync.RWMutex
	state    State
	path     string // persistence file path
	auditLog *AuditLog
}

// New creates a Manager, loading persisted state if it exists.
func New(statePath string, auditLog *AuditLog) (*Manager, error) {
	m := &Manager{
		path:     statePath,
		auditLog: auditLog,
	}
	if err := m.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load CDM state: %w", err)
	}
	// Expire stale sessions on startup
	m.checkExpiry()
	return m, nil
}

// AuditLog returns the CDM audit logger.
func (m *Manager) AuditLog() *AuditLog {
	return m.auditLog
}

// Enabled returns whether CDM is enabled.
func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Enabled
}

// SessionActive returns whether an active (non-expired) session exists.
func (m *Manager) SessionActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkExpiryLocked()
	return m.state.SessionActive
}

// SessionExpiresAt returns the session expiry time, or nil.
func (m *Manager) SessionExpiresAt() *time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.SessionExpiresAt
}

// Snapshot returns a copy of the current state (checking expiry first).
func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkExpiryLocked()
	return m.state
}

// Enable turns on CDM. Jobs will be held until a session is granted.
func (m *Manager) Enable(actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.state.Enabled
	m.state.Enabled = true
	if err := m.persistLocked(); err != nil {
		m.state.Enabled = old
		return err
	}

	if m.auditLog != nil && !old {
		m.auditLog.Write(AuditEntry{
			Action:   "cdm.enable",
			Actor:    actor,
			OldState: "disabled",
			NewState: "enabled",
		})
	}
	return nil
}

// Disable turns off CDM and revokes any active session.
func (m *Manager) Disable(actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.state.Enabled
	hadSession := m.state.SessionActive

	m.state.Enabled = false
	m.state.SessionActive = false
	m.state.SessionExpiresAt = nil
	m.state.SessionGrantedBy = ""
	m.state.SessionGrantedAt = nil

	if err := m.persistLocked(); err != nil {
		return err
	}

	if m.auditLog != nil && old {
		m.auditLog.Write(AuditEntry{
			Action:   "cdm.disable",
			Actor:    actor,
			OldState: stateLabel(old, hadSession),
			NewState: "disabled",
		})
	}
	return nil
}

// GrantSession starts a timed CDM session.
func (m *Manager) GrantSession(actor string, duration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.state.Enabled {
		return fmt.Errorf("CDM is not enabled")
	}

	now := time.Now().UTC()
	expires := now.Add(duration)

	m.state.SessionActive = true
	m.state.SessionExpiresAt = &expires
	m.state.SessionGrantedBy = actor
	m.state.SessionGrantedAt = &now

	if err := m.persistLocked(); err != nil {
		return err
	}

	if m.auditLog != nil {
		m.auditLog.Write(AuditEntry{
			Action:    "cdm.session.grant",
			Actor:     actor,
			Duration:  duration.String(),
			ExpiresAt: &expires,
		})
	}
	return nil
}

// RevokeSession immediately ends an active session.
// In-flight jobs are allowed to complete.
func (m *Manager) RevokeSession(actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.state.SessionActive {
		return fmt.Errorf("no active session to revoke")
	}

	m.state.SessionActive = false
	m.state.SessionExpiresAt = nil
	m.state.SessionGrantedBy = ""
	m.state.SessionGrantedAt = nil

	if err := m.persistLocked(); err != nil {
		return err
	}

	if m.auditLog != nil {
		m.auditLog.Write(AuditEntry{
			Action: "cdm.session.revoke",
			Actor:  actor,
		})
	}
	return nil
}

// CanExecuteJob returns true if a job is allowed to start.
// CDM disabled → always allowed. CDM enabled → only if session active.
func (m *Manager) CanExecuteJob() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkExpiryLocked()
	if !m.state.Enabled {
		return true
	}
	return m.state.SessionActive
}

// checkExpiry expires the session if past its deadline (public, takes lock).
func (m *Manager) checkExpiry() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkExpiryLocked()
}

// checkExpiryLocked must be called with mu held.
func (m *Manager) checkExpiryLocked() {
	if !m.state.SessionActive || m.state.SessionExpiresAt == nil {
		return
	}
	if time.Now().UTC().After(*m.state.SessionExpiresAt) {
		m.state.SessionActive = false
		m.state.SessionExpiresAt = nil
		grantedBy := m.state.SessionGrantedBy
		m.state.SessionGrantedBy = ""
		m.state.SessionGrantedAt = nil
		_ = m.persistLocked()

		if m.auditLog != nil {
			m.auditLog.Write(AuditEntry{
				Action: "cdm.session.expired",
				Actor:  grantedBy,
			})
		}
	}
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path) //nolint:gosec // operator-controlled path
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.state)
}

func (m *Manager) persistLocked() error {
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal CDM state: %w", err)
	}
	return os.WriteFile(m.path, data, 0o600)
}

func stateLabel(enabled, sessionActive bool) string {
	if !enabled {
		return "disabled"
	}
	if sessionActive {
		return "enabled+session"
	}
	return "enabled"
}
