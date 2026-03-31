package update

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// PendingUpdate is written before restart so the new binary can verify itself.
type PendingUpdate struct {
	JobID           string    `json:"job_id"`
	ExpectedVersion string    `json:"expected_version"`
	PreviousVersion string    `json:"previous_version"`
	Deadline        time.Time `json:"deadline"`
}

// WritePending writes the pending update file atomically.
func WritePending(path string, p *PendingUpdate) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending update: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write pending update: %w", err)
	}
	return nil
}

// ReadPending reads and parses the pending update file. Returns nil if file does not exist.
func ReadPending(path string) (*PendingUpdate, error) {
	data, err := os.ReadFile(path) //nolint:gosec // controlled path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pending update: %w", err)
	}

	var p PendingUpdate
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse pending update: %w", err)
	}
	return &p, nil
}

// RemovePending deletes the pending update file. Ignores "not exist" errors.
func RemovePending(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
