package jobs

import (
	"testing"

	"github.com/eavalenzuela/Moebius/shared/models"
)

func TestValidateTransition_AllValid(t *testing.T) {
	valid := []struct {
		from, to string
	}{
		// PENDING transitions
		{models.JobStatusPending, models.JobStatusQueued},
		{models.JobStatusPending, models.JobStatusCancelled},
		// QUEUED transitions
		{models.JobStatusQueued, models.JobStatusDispatched},
		{models.JobStatusQueued, models.JobStatusCDMHold},
		{models.JobStatusQueued, models.JobStatusCancelled},
		// CDM_HOLD transitions
		{models.JobStatusCDMHold, models.JobStatusDispatched},
		{models.JobStatusCDMHold, models.JobStatusCancelled},
		// DISPATCHED transitions
		{models.JobStatusDispatched, models.JobStatusAcknowledged},
		{models.JobStatusDispatched, models.JobStatusQueued},
		{models.JobStatusDispatched, models.JobStatusCancelled},
		// ACKNOWLEDGED transitions
		{models.JobStatusAcknowledged, models.JobStatusRunning},
		{models.JobStatusAcknowledged, models.JobStatusQueued},
		// RUNNING transitions
		{models.JobStatusRunning, models.JobStatusCompleted},
		{models.JobStatusRunning, models.JobStatusFailed},
		{models.JobStatusRunning, models.JobStatusTimedOut},
	}

	for _, tc := range valid {
		if err := ValidateTransition(tc.from, tc.to); err != nil {
			t.Errorf("expected valid transition %s→%s, got error: %v", tc.from, tc.to, err)
		}
	}
}

func TestValidateTransition_TerminalStatesReject(t *testing.T) {
	terminals := []string{
		models.JobStatusCompleted,
		models.JobStatusFailed,
		models.JobStatusTimedOut,
		models.JobStatusCancelled,
	}

	for _, from := range terminals {
		for _, to := range AllStatuses() {
			err := ValidateTransition(from, to)
			if err == nil {
				t.Errorf("expected error for transition from terminal %s→%s", from, to)
			}
		}
	}
}

func TestValidateTransition_InvalidTransitions(t *testing.T) {
	invalid := []struct {
		from, to string
	}{
		{models.JobStatusPending, models.JobStatusRunning},
		{models.JobStatusPending, models.JobStatusCompleted},
		{models.JobStatusQueued, models.JobStatusRunning},
		{models.JobStatusQueued, models.JobStatusCompleted},
		{models.JobStatusRunning, models.JobStatusQueued},
		{models.JobStatusRunning, models.JobStatusPending},
		{models.JobStatusDispatched, models.JobStatusCompleted},
		{models.JobStatusAcknowledged, models.JobStatusCancelled},
	}

	for _, tc := range invalid {
		err := ValidateTransition(tc.from, tc.to)
		if err == nil {
			t.Errorf("expected error for invalid transition %s→%s", tc.from, tc.to)
		}
	}
}

func TestValidateTransition_UnknownState(t *testing.T) {
	err := ValidateTransition("bogus", models.JobStatusQueued)
	if err == nil {
		t.Error("expected error for unknown source state")
	}
}

func TestIsCancellable(t *testing.T) {
	cancellable := []string{
		models.JobStatusPending,
		models.JobStatusQueued,
		models.JobStatusCDMHold,
		models.JobStatusDispatched,
	}
	for _, s := range cancellable {
		if !IsCancellable(s) {
			t.Errorf("expected %s to be cancellable", s)
		}
	}

	notCancellable := []string{
		models.JobStatusAcknowledged,
		models.JobStatusRunning,
		models.JobStatusCompleted,
		models.JobStatusFailed,
		models.JobStatusTimedOut,
		models.JobStatusCancelled,
	}
	for _, s := range notCancellable {
		if IsCancellable(s) {
			t.Errorf("expected %s to not be cancellable", s)
		}
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(models.JobStatusFailed) {
		t.Error("expected failed to be retryable")
	}
	if !IsRetryable(models.JobStatusTimedOut) {
		t.Error("expected timed_out to be retryable")
	}
	for _, s := range []string{
		models.JobStatusPending, models.JobStatusQueued, models.JobStatusRunning,
		models.JobStatusCompleted, models.JobStatusCancelled,
	} {
		if IsRetryable(s) {
			t.Errorf("expected %s to not be retryable", s)
		}
	}
}

func TestAllStatuses_Count(t *testing.T) {
	statuses := AllStatuses()
	if len(statuses) != 10 {
		t.Errorf("expected 10 statuses, got %d", len(statuses))
	}
}

// P2: Verify CancellableStatuses are actually cancellable via ValidateTransition.
func TestCancellableStatuses_ConsistentWithTransitions(t *testing.T) {
	for _, s := range CancellableStatuses() {
		if err := ValidateTransition(s, models.JobStatusCancelled); err != nil {
			t.Errorf("CancellableStatuses includes %q but ValidateTransition rejects %s→cancelled: %v", s, s, err)
		}
	}
}

// P2: Verify non-cancellable statuses are NOT in the transition map for cancelled.
func TestNonCancellableStatuses_RejectCancelled(t *testing.T) {
	cancellable := make(map[string]bool)
	for _, s := range CancellableStatuses() {
		cancellable[s] = true
	}
	for _, s := range AllStatuses() {
		if cancellable[s] {
			continue
		}
		if err := ValidateTransition(s, models.JobStatusCancelled); err == nil {
			t.Errorf("status %q is not in CancellableStatuses but ValidateTransition allows %s→cancelled", s, s)
		}
	}
}

// P2: ShouldRetry edge cases.
func TestShouldRetry_ZeroMaxRetries(t *testing.T) {
	// Even with zero retries allowed, retryCount 0 < maxRetries 0 is false
	if ShouldRetry(models.JobStatusFailed, 0, 0) {
		t.Error("ShouldRetry(failed, 0, 0) should be false")
	}
}

func TestShouldRetry_BoundaryRetryCount(t *testing.T) {
	// retryCount = maxRetries - 1 should allow one more retry
	if !ShouldRetry(models.JobStatusFailed, 2, 3) {
		t.Error("ShouldRetry(failed, 2, 3) should be true")
	}
	// retryCount = maxRetries should not allow retry
	if ShouldRetry(models.JobStatusFailed, 3, 3) {
		t.Error("ShouldRetry(failed, 3, 3) should be false")
	}
}

func TestShouldRetry_AllNonRetryableStatuses(t *testing.T) {
	nonRetryable := []string{
		models.JobStatusPending,
		models.JobStatusQueued,
		models.JobStatusCDMHold,
		models.JobStatusDispatched,
		models.JobStatusAcknowledged,
		models.JobStatusRunning,
		models.JobStatusCompleted,
		models.JobStatusCancelled,
	}
	for _, s := range nonRetryable {
		if ShouldRetry(s, 0, 10) {
			t.Errorf("ShouldRetry(%s, 0, 10) should be false", s)
		}
	}
}
