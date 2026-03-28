package models

import "testing"

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{
		JobStatusCompleted,
		JobStatusFailed,
		JobStatusTimedOut,
		JobStatusCancelled,
	}
	for _, s := range terminal {
		if !IsTerminalStatus(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}

	nonTerminal := []string{
		JobStatusPending,
		JobStatusQueued,
		JobStatusCDMHold,
		JobStatusDispatched,
		JobStatusAcknowledged,
		JobStatusRunning,
	}
	for _, s := range nonTerminal {
		if IsTerminalStatus(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}
