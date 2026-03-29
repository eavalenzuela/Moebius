package cdm

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AuditEntry represents a single CDM audit log entry.
type AuditEntry struct {
	Timestamp time.Time  `json:"timestamp"`
	Action    string     `json:"action"`
	Actor     string     `json:"actor,omitempty"`
	OldState  string     `json:"old_state,omitempty"`
	NewState  string     `json:"new_state,omitempty"`
	Duration  string     `json:"duration,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	JobID     string     `json:"job_id,omitempty"`
	JobType   string     `json:"job_type,omitempty"`
}

// AuditLog is an append-only local audit log for CDM events.
// The file is created with 0600 permissions (root/owner only).
type AuditLog struct {
	mu   sync.Mutex
	path string
}

// NewAuditLog creates an AuditLog. The file is created on first write.
func NewAuditLog(path string) *AuditLog {
	return &AuditLog{path: path}
}

// Write appends an entry to the audit log.
func (a *AuditLog) Write(entry AuditEntry) {
	entry.Timestamp = time.Now().UTC()

	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-controlled path
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	_, _ = f.Write(data)
}

// LogJobExecution records that a job was executed during a CDM session.
func (a *AuditLog) LogJobExecution(jobID, jobType, actor string) {
	a.Write(AuditEntry{
		Action:  "cdm.job.executed",
		Actor:   actor,
		JobID:   jobID,
		JobType: jobType,
	})
}

// Read returns the last N audit entries (most recent first).
// Returns an empty slice if the file doesn't exist.
func (a *AuditLog) Read(limit int) ([]AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.path) //nolint:gosec // operator-controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read CDM audit log: %w", err)
	}

	var all []AuditEntry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal(line, &e); err == nil {
			all = append(all, e)
		}
	}

	// Return most recent first
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, nil
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
