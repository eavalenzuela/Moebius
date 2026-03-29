package jobs

import (
	"testing"

	"github.com/moebius-oss/moebius/shared/models"
)

func TestValidateType_AllKnown(t *testing.T) {
	types := []string{
		models.JobTypeExec,
		models.JobTypePackageInstall,
		models.JobTypePackageRemove,
		models.JobTypePackageUpdate,
		models.JobTypeInventoryFull,
		models.JobTypeFileTransfer,
		models.JobTypeAgentUpdate,
		models.JobTypeAgentRollback,
	}
	for _, jt := range types {
		if err := ValidateType(jt); err != nil {
			t.Errorf("expected %q to be valid: %v", jt, err)
		}
	}
}

func TestValidateType_Unknown(t *testing.T) {
	if err := ValidateType("bogus"); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestDefaultRetryPolicy_HasPolicy(t *testing.T) {
	cases := []struct {
		jobType    string
		maxRetries int
	}{
		{models.JobTypePackageInstall, 3},
		{models.JobTypePackageRemove, 3},
		{models.JobTypePackageUpdate, 3},
		{models.JobTypeInventoryFull, 5},
		{models.JobTypeFileTransfer, 3},
		{models.JobTypeAgentUpdate, 2},
	}
	for _, tc := range cases {
		p := DefaultRetryPolicy(tc.jobType)
		if p == nil {
			t.Fatalf("expected default retry policy for %s", tc.jobType)
		}
		if p.MaxRetries != tc.maxRetries {
			t.Errorf("%s: expected max_retries=%d, got %d", tc.jobType, tc.maxRetries, p.MaxRetries)
		}
	}
}

func TestDefaultRetryPolicy_NoPolicy(t *testing.T) {
	noPolicyTypes := []string{
		models.JobTypeExec,
		models.JobTypeAgentRollback,
	}
	for _, jt := range noPolicyTypes {
		if p := DefaultRetryPolicy(jt); p != nil {
			t.Errorf("expected nil policy for %s, got %+v", jt, p)
		}
	}
}

func TestDefaultRetryPolicy_ReturnsCopy(t *testing.T) {
	p1 := DefaultRetryPolicy(models.JobTypePackageInstall)
	p2 := DefaultRetryPolicy(models.JobTypePackageInstall)
	if p1 == p2 {
		t.Error("expected different pointers (copy, not reference)")
	}
}

func TestShouldRetry(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		count, max int
		want       bool
	}{
		{"failed with retries left", models.JobStatusFailed, 0, 3, true},
		{"failed exhausted", models.JobStatusFailed, 3, 3, false},
		{"timed_out with retries left", models.JobStatusTimedOut, 1, 2, true},
		{"timed_out exhausted", models.JobStatusTimedOut, 2, 2, false},
		{"completed", models.JobStatusCompleted, 0, 3, false},
		{"cancelled", models.JobStatusCancelled, 0, 3, false},
		{"running", models.JobStatusRunning, 0, 3, false},
	}
	for _, tc := range cases {
		got := ShouldRetry(tc.status, tc.count, tc.max)
		if got != tc.want {
			t.Errorf("%s: ShouldRetry(%s, %d, %d) = %v, want %v",
				tc.name, tc.status, tc.count, tc.max, got, tc.want)
		}
	}
}
