package quota

import (
	"context"
	"errors"
	"testing"

	"github.com/eavalenzuela/Moebius/shared/models"
)

func TestApplyOverride(t *testing.T) {
	base := Defaults{
		MaxDevices:       100,
		MaxQueuedJobs:    200,
		MaxAPIKeys:       10,
		MaxFileSizeBytes: 1024,
	}

	cases := []struct {
		name     string
		override models.TenantQuotas
		want     Defaults
	}{
		{
			name:     "empty override inherits all defaults",
			override: models.TenantQuotas{},
			want:     base,
		},
		{
			name:     "override replaces single field",
			override: models.TenantQuotas{MaxDevices: 5},
			want:     Defaults{MaxDevices: 5, MaxQueuedJobs: 200, MaxAPIKeys: 10, MaxFileSizeBytes: 1024},
		},
		{
			name: "override replaces all fields",
			override: models.TenantQuotas{
				MaxDevices: 1, MaxQueuedJobs: 2, MaxAPIKeys: 3, MaxFileSizeBytes: 4,
			},
			want: Defaults{MaxDevices: 1, MaxQueuedJobs: 2, MaxAPIKeys: 3, MaxFileSizeBytes: 4},
		},
		{
			// -1 means "unlimited" — non-zero so it replaces the default.
			name:     "negative override replaces default with unlimited",
			override: models.TenantQuotas{MaxAPIKeys: -1},
			want:     Defaults{MaxDevices: 100, MaxQueuedJobs: 200, MaxAPIKeys: -1, MaxFileSizeBytes: 1024},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyOverride(base, tc.override)
			if got != tc.want {
				t.Errorf("ApplyOverride() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestErrExceeded_MessageFormat(t *testing.T) {
	// Single-item create — Attempted is 0, message omits the attempted clause
	single := &ErrExceeded{Kind: KindAPIKeys, Limit: 5, Current: 5}
	if got := single.Error(); got != "quota api_keys exceeded: current=5 limit=5" {
		t.Errorf("single-item message = %q", got)
	}

	// Batch create — Attempted is non-zero, message includes it
	batch := &ErrExceeded{Kind: KindQueuedJobs, Limit: 10, Current: 9, Attempted: 3}
	if got := batch.Error(); got != "quota queued_jobs exceeded: current=9 attempted=3 limit=10" {
		t.Errorf("batch message = %q", got)
	}
}

func TestAsExceeded(t *testing.T) {
	orig := &ErrExceeded{Kind: KindDevices, Limit: 1, Current: 1}
	wrapped := errors.Join(errors.New("context"), orig)

	got, ok := AsExceeded(wrapped)
	if !ok {
		t.Fatalf("AsExceeded did not match wrapped ErrExceeded")
	}
	if got != orig {
		t.Errorf("AsExceeded returned wrong pointer")
	}

	if _, ok := AsExceeded(errors.New("other")); ok {
		t.Error("AsExceeded matched a non-quota error")
	}
}

func TestNilResolver_NoopChecks(t *testing.T) {
	// A nil *Resolver must be usable as a no-op.
	var r *Resolver
	ctx := context.Background()
	if err := r.CheckDevices(ctx, "tenant"); err != nil {
		t.Errorf("CheckDevices on nil: %v", err)
	}
	if err := r.CheckQueuedJobs(ctx, "tenant", 5); err != nil {
		t.Errorf("CheckQueuedJobs on nil: %v", err)
	}
	if err := r.CheckAPIKeys(ctx, "tenant"); err != nil {
		t.Errorf("CheckAPIKeys on nil: %v", err)
	}
	if err := r.CheckFileSize(ctx, "tenant", 1<<40); err != nil {
		t.Errorf("CheckFileSize on nil: %v", err)
	}
	if got := r.GlobalDefaults(); got != (Defaults{}) {
		t.Errorf("GlobalDefaults on nil = %+v, want zero", got)
	}
}
