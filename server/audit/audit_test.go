package audit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/eavalenzuela/Moebius/server/metrics"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5/pgxpool"
	dto "github.com/prometheus/client_model/go"
)

// TestLogAction_FailureCountedAndLogged verifies that audit write failures
// are observable via both the structured log AND the Prometheus counter —
// not silently swallowed as they were when call sites used `_ = LogAction(...)`.
// This is the core property Item 5 is defending.
func TestLogAction_FailureCountedAndLogged(t *testing.T) {
	// Build a pool pointed at an unreachable database and close it so the
	// first Exec is guaranteed to fail without waiting on a real network.
	cfg, err := pgxpool.ParseConfig("postgres://invalid:invalid@127.0.0.1:1/invalid?connect_timeout=1")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	pool.Close() // guarantee subsequent operations return an error

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l := New(pool, log)

	before := counterValue(t, metrics.AuditWriteFailuresTotal)

	l.LogAction(context.Background(), "tenant-x", "actor-y", models.ActorTypeUser,
		"user.update_role", "user", "user-z", map[string]string{"role_id": "r"})

	after := counterValue(t, metrics.AuditWriteFailuresTotal)
	if after-before != 1 {
		t.Errorf("audit_write_failures_total delta = %v, want 1", after-before)
	}
	if !strings.Contains(buf.String(), "audit log write failed") {
		t.Errorf("expected failure log line, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "user.update_role") {
		t.Errorf("expected action field in log, got:\n%s", buf.String())
	}
}

func counterValue(t *testing.T, c interface {
	Write(*dto.Metric) error
}) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("counter write: %v", err)
	}
	return m.GetCounter().GetValue()
}
