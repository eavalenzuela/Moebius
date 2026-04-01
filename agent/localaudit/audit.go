// Package localaudit provides the agent's local audit log covering all
// local interface events: authentication, CDM operations, and config views.
// The log file is created with 0600 permissions (root/SYSTEM only).
package localaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Event actions.
const (
	ActionAuthSuccess    = "auth.success"
	ActionAuthFailure    = "auth.failure"
	ActionCDMToggle      = "cdm.toggle"
	ActionCDMGrant       = "cdm.session.grant"
	ActionCDMRevoke      = "cdm.session.revoke"
	ActionConfigView     = "config.view"
)

// Interface sources.
const (
	InterfaceCLI = "cli"
	InterfaceUI  = "ui"
)

// Entry represents a single local audit log entry.
type Entry struct {
	Timestamp time.Time  `json:"timestamp"`
	Action    string     `json:"action"`
	Username  string     `json:"username,omitempty"`
	Interface string     `json:"interface,omitempty"` // "cli" or "ui"
	OldState  string     `json:"old_state,omitempty"`
	NewState  string     `json:"new_state,omitempty"`
	Duration  string     `json:"duration,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Reason    string     `json:"reason,omitempty"` // failure or revocation reason
}

// Logger is an append-only local audit logger.
// The log file is created with 0600 permissions on first write.
type Logger struct {
	mu   sync.Mutex
	path string
}

// New creates a Logger that writes to the given path.
func New(path string) *Logger {
	return &Logger{path: path}
}

// Write appends an entry to the audit log.
func (l *Logger) Write(entry Entry) {
	entry.Timestamp = time.Now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // agent-controlled path
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	_, _ = f.Write(data)
}

// LogAuthSuccess records a successful authentication.
func (l *Logger) LogAuthSuccess(username, iface string) {
	l.Write(Entry{
		Action:    ActionAuthSuccess,
		Username:  username,
		Interface: iface,
	})
}

// LogAuthFailure records a failed authentication attempt.
func (l *Logger) LogAuthFailure(username, iface, reason string) {
	l.Write(Entry{
		Action:    ActionAuthFailure,
		Username:  username,
		Interface: iface,
		Reason:    reason,
	})
}

// LogCDMToggle records CDM enable/disable.
func (l *Logger) LogCDMToggle(username, iface, oldState, newState string) {
	l.Write(Entry{
		Action:    ActionCDMToggle,
		Username:  username,
		Interface: iface,
		OldState:  oldState,
		NewState:  newState,
	})
}

// LogCDMGrant records a CDM session grant.
func (l *Logger) LogCDMGrant(username, iface, duration string, expiresAt *time.Time) {
	l.Write(Entry{
		Action:    ActionCDMGrant,
		Username:  username,
		Interface: iface,
		Duration:  duration,
		ExpiresAt: expiresAt,
	})
}

// LogCDMRevoke records a CDM session revocation.
func (l *Logger) LogCDMRevoke(username, iface, reason string) {
	l.Write(Entry{
		Action:    ActionCDMRevoke,
		Username:  username,
		Interface: iface,
		Reason:    reason,
	})
}

// LogConfigView records that a user viewed the agent config.
func (l *Logger) LogConfigView(username, iface string) {
	l.Write(Entry{
		Action:    ActionConfigView,
		Username:  username,
		Interface: iface,
	})
}

// Read returns the last limit entries (most recent first).
func (l *Logger) Read(limit int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.path) //nolint:gosec // agent-controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	var all []Entry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err == nil {
			all = append(all, e)
		}
	}

	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	// Most recent first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, nil
}

// ReadCDMOnly returns only CDM-related entries (filtered view for local users).
func (l *Logger) ReadCDMOnly(limit int) ([]Entry, error) {
	all, err := l.Read(0) // get all, then filter
	if err != nil {
		return nil, err
	}

	var filtered []Entry
	for _, e := range all {
		switch e.Action {
		case ActionCDMToggle, ActionCDMGrant, ActionCDMRevoke:
			filtered = append(filtered, e)
		}
	}

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
