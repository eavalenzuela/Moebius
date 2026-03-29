package jobs

import (
	"fmt"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// validTransitions defines the allowed state transitions for the job state machine.
var validTransitions = map[string][]string{
	models.JobStatusPending:      {models.JobStatusQueued, models.JobStatusCancelled},
	models.JobStatusQueued:       {models.JobStatusDispatched, models.JobStatusCDMHold, models.JobStatusCancelled},
	models.JobStatusCDMHold:      {models.JobStatusDispatched, models.JobStatusCancelled},
	models.JobStatusDispatched:   {models.JobStatusAcknowledged, models.JobStatusQueued, models.JobStatusCancelled},
	models.JobStatusAcknowledged: {models.JobStatusRunning, models.JobStatusQueued},
	models.JobStatusRunning:      {models.JobStatusCompleted, models.JobStatusFailed, models.JobStatusTimedOut},
}

// ValidateTransition checks whether a state transition is allowed.
func ValidateTransition(from, to string) error {
	targets, ok := validTransitions[from]
	if !ok {
		if models.IsTerminalStatus(from) {
			return fmt.Errorf("cannot transition from terminal state %q", from)
		}
		return fmt.Errorf("unknown state %q", from)
	}
	for _, t := range targets {
		if t == to {
			return nil
		}
	}
	return fmt.Errorf("invalid transition from %q to %q", from, to)
}

// AllStatuses returns all valid job statuses.
func AllStatuses() []string {
	return []string{
		models.JobStatusPending,
		models.JobStatusQueued,
		models.JobStatusCDMHold,
		models.JobStatusDispatched,
		models.JobStatusAcknowledged,
		models.JobStatusRunning,
		models.JobStatusCompleted,
		models.JobStatusFailed,
		models.JobStatusTimedOut,
		models.JobStatusCancelled,
	}
}

// CancellableStatuses returns statuses from which a job can be cancelled.
func CancellableStatuses() []string {
	return []string{
		models.JobStatusPending,
		models.JobStatusQueued,
		models.JobStatusCDMHold,
		models.JobStatusDispatched,
	}
}

// IsCancellable returns true if a job in the given status can be cancelled.
func IsCancellable(status string) bool {
	for _, s := range CancellableStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// IsRetryable returns true if the status allows automatic retry.
func IsRetryable(status string) bool {
	return status == models.JobStatusFailed || status == models.JobStatusTimedOut
}
