//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/server/notify"
	"github.com/eavalenzuela/Moebius/server/scheduler"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// Reaper invariant: stuck jobs must not linger forever. Dispatched jobs that
// never get acknowledged get requeued; acknowledged/running jobs that never
// finish get marked timed_out. These tests exercise the reaper directly
// against a real database — the SQL predicates are the thing under test.

// exposeReaperForTest exposes the private reap methods via the Scheduler
// itself — we call Scheduler methods through a wrapper to avoid exporting
// internals.
func newReaperScheduler(t *testing.T, h *testHarness, dispatched, inflight time.Duration) *scheduler.Scheduler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := notify.New(nil, log)
	return scheduler.New(h.pool, h.store, notifier, log, scheduler.Config{
		TickSeconds:                30,
		ReaperDispatchedTimeoutSec: int(dispatched.Seconds()),
		ReaperInflightTimeoutSec:   int(inflight.Seconds()),
	})
}

// insertJob creates a job row directly, bypassing the API. status and
// *_at fields control where the reaper should find it.
func insertJob(t *testing.T, h *testHarness, deviceID, status string, dispatchedAt, acknowledgedAt, startedAt *time.Time) string {
	t.Helper()
	jobID := models.NewJobID()
	_, err := h.pool.Exec(context.Background(),
		`INSERT INTO jobs (id, tenant_id, device_id, type, status, payload, max_retries, dispatched_at, acknowledged_at, started_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8, $9, $10)`,
		jobID, h.tenantID, deviceID, models.JobTypeExec, status, []byte(`{}`),
		dispatchedAt, acknowledgedAt, startedAt, time.Now().UTC())
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	return jobID
}

// jobStatus fetches a job's status + last_error.
func jobStatus(t *testing.T, h *testHarness, jobID string) (string, string) {
	t.Helper()
	var status, lastErr string
	err := h.pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(last_error, '') FROM jobs WHERE id = $1`, jobID).
		Scan(&status, &lastErr)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	return status, lastErr
}

func TestReaper_RequeuesStuckDispatchedJobs(t *testing.T) {
	h := newHarness(t)

	// Create a device to own the jobs
	deviceID, _, _ := h.enrollAgent("reaper-host-dispatched")

	old := time.Now().UTC().Add(-10 * time.Minute)
	fresh := time.Now().UTC().Add(-30 * time.Second)

	staleJob := insertJob(t, h, deviceID, models.JobStatusDispatched, &old, nil, nil)
	freshJob := insertJob(t, h, deviceID, models.JobStatusDispatched, &fresh, nil, nil)

	// 5-minute dispatched timeout; fresh is 30s old, stale is 10min old
	sched := newReaperScheduler(t, h, 5*time.Minute, 1*time.Hour)
	sched.RunTickOnce(context.Background())

	if s, _ := jobStatus(t, h, staleJob); s != models.JobStatusQueued {
		t.Errorf("stale dispatched job status = %q, want %q", s, models.JobStatusQueued)
	}
	if s, _ := jobStatus(t, h, freshJob); s != models.JobStatusDispatched {
		t.Errorf("fresh dispatched job status = %q, want %q (not past timeout)", s, models.JobStatusDispatched)
	}

	// Verify dispatched_at was cleared on requeue so the job can be re-dispatched
	var disp *time.Time
	_ = h.pool.QueryRow(context.Background(),
		`SELECT dispatched_at FROM jobs WHERE id = $1`, staleJob).Scan(&disp)
	if disp != nil {
		t.Errorf("stale job's dispatched_at should be NULL after requeue, got %v", disp)
	}
}

func TestReaper_TimesOutStuckInflightJobs(t *testing.T) {
	h := newHarness(t)

	deviceID, _, _ := h.enrollAgent("reaper-host-inflight")

	old := time.Now().UTC().Add(-2 * time.Hour)
	fresh := time.Now().UTC().Add(-5 * time.Minute)

	// acknowledged, very old — should time out
	ackStale := insertJob(t, h, deviceID, models.JobStatusAcknowledged, &old, &old, nil)
	// running, started long ago — should time out
	runStale := insertJob(t, h, deviceID, models.JobStatusRunning, &old, &old, &old)
	// acknowledged, recently — should stay
	ackFresh := insertJob(t, h, deviceID, models.JobStatusAcknowledged, &fresh, &fresh, nil)

	// 1-hour inflight timeout
	sched := newReaperScheduler(t, h, 5*time.Minute, 1*time.Hour)
	sched.RunTickOnce(context.Background())

	if s, le := jobStatus(t, h, ackStale); s != models.JobStatusTimedOut {
		t.Errorf("stale ack job status = %q, want timed_out; last_error=%q", s, le)
	} else if le == "" {
		t.Error("expected last_error to be set on timed-out job")
	}
	if s, _ := jobStatus(t, h, runStale); s != models.JobStatusTimedOut {
		t.Errorf("stale running job status = %q, want timed_out", s)
	}
	if s, _ := jobStatus(t, h, ackFresh); s != models.JobStatusAcknowledged {
		t.Errorf("fresh ack job status = %q, want acknowledged (not past timeout)", s)
	}
}

func TestReaper_DeletesExpiredEnrollmentTokens(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// expired, unused — should be deleted
	expiredID := models.NewEnrollmentTokenID()
	_, err := h.pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (id, tenant_id, token_hash, created_by, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		expiredID, h.tenantID, "hash-expired", h.userID, now.Add(-1*time.Hour), now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("insert expired token: %v", err)
	}

	// expired but USED — should NOT be deleted (audit trail)
	usedID := models.NewEnrollmentTokenID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (id, tenant_id, token_hash, created_by, expires_at, used_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		usedID, h.tenantID, "hash-used", h.userID, now.Add(-1*time.Hour), now.Add(-90*time.Minute), now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("insert used token: %v", err)
	}

	// fresh, unused — should NOT be deleted
	freshID := models.NewEnrollmentTokenID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (id, tenant_id, token_hash, created_by, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		freshID, h.tenantID, "hash-fresh", h.userID, now.Add(1*time.Hour), now)
	if err != nil {
		t.Fatalf("insert fresh token: %v", err)
	}

	sched := newReaperScheduler(t, h, 5*time.Minute, 1*time.Hour)
	sched.RunTickOnce(context.Background())

	exists := func(id string) bool {
		var n int
		_ = h.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM enrollment_tokens WHERE id = $1`, id).Scan(&n)
		return n > 0
	}
	if exists(expiredID) {
		t.Error("expected expired unused token to be deleted")
	}
	if !exists(usedID) {
		t.Error("expected used token to be retained for audit")
	}
	if !exists(freshID) {
		t.Error("expected fresh token to be retained")
	}
}
