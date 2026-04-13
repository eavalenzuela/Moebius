// Package quota enforces per-tenant resource count and size ceilings.
//
// Quotas are a separate concern from the rate limiter in server/ratelimit:
// rate limiting caps the *frequency* of requests, whereas quotas cap the
// *accumulated* tenant footprint (devices, queued work, API keys, file
// size). Both run simultaneously.
//
// Enforcement is best-effort: each check reads a COUNT(*) outside any
// transaction, so two concurrent creates against an almost-full tenant
// can both pass the pre-check and land one item over the limit. For
// DoS mitigation that's acceptable — the next request will be rejected,
// and rate limiting provides the tight inner bound.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// Kind names a resource that can have a per-tenant ceiling.
type Kind string

const (
	KindDevices    Kind = "devices"
	KindQueuedJobs Kind = "queued_jobs"
	KindAPIKeys    Kind = "api_keys"
	KindFileSize   Kind = "file_size_bytes"
)

// ErrExceeded reports a quota violation.
type ErrExceeded struct {
	Kind      Kind
	Limit     int64
	Current   int64
	Attempted int64 // items/bytes the caller tried to add; 0 for single-item creates
}

func (e *ErrExceeded) Error() string {
	if e.Attempted > 0 {
		return fmt.Sprintf("quota %s exceeded: current=%d attempted=%d limit=%d",
			e.Kind, e.Current, e.Attempted, e.Limit)
	}
	return fmt.Sprintf("quota %s exceeded: current=%d limit=%d",
		e.Kind, e.Current, e.Limit)
}

// AsExceeded returns the ErrExceeded if err wraps one, and a boolean
// indicating whether it matched. Callers use this to distinguish
// quota rejections (client error, 409) from internal failures (500).
func AsExceeded(err error) (*ErrExceeded, bool) {
	var e *ErrExceeded
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Defaults is the cluster-wide per-tenant ceiling applied when the
// tenant does not override the value in TenantConfig.Quotas. A value
// of -1 disables the check (unlimited).
type Defaults struct {
	MaxDevices       int64
	MaxQueuedJobs    int64
	MaxAPIKeys       int64
	MaxFileSizeBytes int64
}

// Resolver enforces per-tenant quotas backed by COUNT(*) queries on
// the live tables. A nil *Resolver is valid and treats every Check as
// a no-op; tests and self-hosted deployments that don't need quotas
// can pass nil into the router without conditional wiring.
type Resolver struct {
	pool     *pgxpool.Pool
	defaults Defaults
}

// NewResolver builds a Resolver. Pass nil for no-op behavior.
func NewResolver(pool *pgxpool.Pool, d Defaults) *Resolver {
	return &Resolver{pool: pool, defaults: d}
}

// Defaults returns the global defaults baked into the resolver. For
// logging and tests only — live checks always re-read tenant config.
func (r *Resolver) GlobalDefaults() Defaults {
	if r == nil {
		return Defaults{}
	}
	return r.defaults
}

// Effective returns the per-tenant limits after overlaying overrides
// from TenantConfig.Quotas. A zero field in the override means
// "inherit default"; -1 means "unlimited".
func (r *Resolver) Effective(ctx context.Context, tenantID string) (Defaults, error) {
	if r == nil {
		return Defaults{MaxDevices: -1, MaxQueuedJobs: -1, MaxAPIKeys: -1, MaxFileSizeBytes: -1}, nil
	}
	var configJSON []byte
	err := r.pool.QueryRow(ctx,
		`SELECT config FROM tenants WHERE id = $1`, tenantID,
	).Scan(&configJSON)
	if err != nil {
		return Defaults{}, fmt.Errorf("load tenant config: %w", err)
	}

	eff := r.defaults
	if len(configJSON) > 0 {
		var cfg models.TenantConfig
		if err := json.Unmarshal(configJSON, &cfg); err == nil && cfg.Quotas != nil {
			eff = ApplyOverride(eff, *cfg.Quotas)
		}
	}
	return eff, nil
}

// ApplyOverride overlays a TenantQuotas onto a Defaults value. A zero
// field in the override inherits the default; a non-zero field
// (including -1 for unlimited) replaces it.
func ApplyOverride(def Defaults, o models.TenantQuotas) Defaults {
	if o.MaxDevices != 0 {
		def.MaxDevices = o.MaxDevices
	}
	if o.MaxQueuedJobs != 0 {
		def.MaxQueuedJobs = o.MaxQueuedJobs
	}
	if o.MaxAPIKeys != 0 {
		def.MaxAPIKeys = o.MaxAPIKeys
	}
	if o.MaxFileSizeBytes != 0 {
		def.MaxFileSizeBytes = o.MaxFileSizeBytes
	}
	return def
}

// CheckDevices returns ErrExceeded if adding one device would push
// the tenant above its device ceiling.
func (r *Resolver) CheckDevices(ctx context.Context, tenantID string) error {
	if r == nil {
		return nil
	}
	eff, err := r.Effective(ctx, tenantID)
	if err != nil {
		return err
	}
	if eff.MaxDevices < 0 {
		return nil
	}
	var count int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM devices WHERE tenant_id = $1`, tenantID,
	).Scan(&count); err != nil {
		return fmt.Errorf("count devices: %w", err)
	}
	if count+1 > eff.MaxDevices {
		return &ErrExceeded{Kind: KindDevices, Limit: eff.MaxDevices, Current: count}
	}
	return nil
}

// CheckQueuedJobs returns ErrExceeded if adding incr queued jobs
// would push the tenant above its queued-job ceiling. Only jobs in
// the queued or dispatched state count — running, completed, failed,
// cancelled, and timed-out jobs are excluded so a long backlog of
// historical work does not pin the cap.
func (r *Resolver) CheckQueuedJobs(ctx context.Context, tenantID string, incr int64) error {
	if r == nil || incr <= 0 {
		return nil
	}
	eff, err := r.Effective(ctx, tenantID)
	if err != nil {
		return err
	}
	if eff.MaxQueuedJobs < 0 {
		return nil
	}
	var count int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM jobs WHERE tenant_id = $1 AND status IN ('queued','dispatched')`,
		tenantID,
	).Scan(&count); err != nil {
		return fmt.Errorf("count queued jobs: %w", err)
	}
	if count+incr > eff.MaxQueuedJobs {
		return &ErrExceeded{Kind: KindQueuedJobs, Limit: eff.MaxQueuedJobs, Current: count, Attempted: incr}
	}
	return nil
}

// CheckAPIKeys returns ErrExceeded if adding one API key would push
// the tenant above its key ceiling.
func (r *Resolver) CheckAPIKeys(ctx context.Context, tenantID string) error {
	if r == nil {
		return nil
	}
	eff, err := r.Effective(ctx, tenantID)
	if err != nil {
		return err
	}
	if eff.MaxAPIKeys < 0 {
		return nil
	}
	var count int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM api_keys WHERE tenant_id = $1`, tenantID,
	).Scan(&count); err != nil {
		return fmt.Errorf("count api keys: %w", err)
	}
	if count+1 > eff.MaxAPIKeys {
		return &ErrExceeded{Kind: KindAPIKeys, Limit: eff.MaxAPIKeys, Current: count}
	}
	return nil
}

// CheckFileSize returns ErrExceeded if sizeBytes exceeds the tenant
// single-file size ceiling. This is a static check — no DB query.
func (r *Resolver) CheckFileSize(ctx context.Context, tenantID string, sizeBytes int64) error {
	if r == nil {
		return nil
	}
	eff, err := r.Effective(ctx, tenantID)
	if err != nil {
		return err
	}
	if eff.MaxFileSizeBytes < 0 {
		return nil
	}
	if sizeBytes > eff.MaxFileSizeBytes {
		return &ErrExceeded{Kind: KindFileSize, Limit: eff.MaxFileSizeBytes, Attempted: sizeBytes}
	}
	return nil
}
