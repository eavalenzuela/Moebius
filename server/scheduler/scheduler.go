package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/eavalenzuela/Moebius/server/notify"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
)

// advisoryLockID is the PG advisory lock key for scheduler leader election.
const advisoryLockID int64 = 0x4d6f65626975735f // "Moebius_"

// cronParser uses standard 5-field cron expressions.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Scheduler evaluates scheduled jobs and alert rules on a tick interval.
type Scheduler struct {
	pool     *pgxpool.Pool
	store    *store.Store
	notifier *notify.Notifier
	log      *slog.Logger
	tick     time.Duration
}

// New creates a Scheduler.
func New(pool *pgxpool.Pool, st *store.Store, notifier *notify.Notifier, log *slog.Logger, tickSeconds int) *Scheduler {
	if tickSeconds <= 0 {
		tickSeconds = 30
	}
	return &Scheduler{
		pool:     pool,
		store:    st,
		notifier: notifier,
		log:      log,
		tick:     time.Duration(tickSeconds) * time.Second,
	}
}

// Run blocks until ctx is cancelled. It acquires a PG advisory lock for leader
// election — only one scheduler instance runs at a time.
func (s *Scheduler) Run(ctx context.Context) error {
	s.log.Info("scheduler starting, attempting leader election")

	// Try to acquire session-level advisory lock (blocks until acquired or ctx cancelled)
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for advisory lock: %w", err)
	}
	defer conn.Release()

	// pg_try_advisory_lock returns true if lock acquired; false if another session holds it.
	// We loop with pg_try so we can respect ctx cancellation.
	for {
		var acquired bool
		err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&acquired)
		if err != nil {
			return fmt.Errorf("advisory lock query: %w", err)
		}
		if acquired {
			s.log.Info("leader lock acquired")
			break
		}
		s.log.Debug("leader lock held by another instance, retrying")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	// Release advisory lock on exit
	defer func() {
		// Use background context since ctx may be cancelled
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(releaseCtx, "SELECT pg_advisory_unlock($1)", advisoryLockID)
		s.log.Info("leader lock released")
	}()

	s.log.Info("scheduler running", slog.Duration("tick", s.tick))
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler shutting down")
			return nil
		case <-ticker.C:
			s.runTick(ctx)
		}
	}
}

func (s *Scheduler) runTick(ctx context.Context) {
	now := time.Now().UTC()

	// Evaluate due scheduled jobs
	s.evaluateScheduledJobs(ctx, now)

	// Evaluate alert rules
	s.evaluateAlertRules(ctx, now)
}

func (s *Scheduler) evaluateScheduledJobs(ctx context.Context, now time.Time) {
	due, err := s.store.ListDueScheduledJobs(ctx, now)
	if err != nil {
		s.log.Error("failed to list due scheduled jobs", slog.String("error", err.Error()))
		return
	}

	for _, sj := range due {
		s.log.Info("executing scheduled job",
			slog.String("scheduled_job_id", sj.ID),
			slog.String("name", sj.Name),
			slog.String("job_type", sj.JobType),
		)

		if err := s.dispatchScheduledJob(ctx, &sj, now); err != nil {
			s.log.Error("failed to dispatch scheduled job",
				slog.String("scheduled_job_id", sj.ID),
				slog.String("error", err.Error()),
			)
			continue
		}

		// Compute next run
		sched, err := cronParser.Parse(sj.CronExpr)
		if err != nil {
			s.log.Error("invalid cron expression on scheduled job",
				slog.String("scheduled_job_id", sj.ID),
				slog.String("cron_expr", sj.CronExpr),
				slog.String("error", err.Error()),
			)
			continue
		}
		nextRun := sched.Next(now)

		if err := s.store.MarkScheduledJobRun(ctx, sj.ID, now, nextRun); err != nil {
			s.log.Error("failed to update scheduled job run times",
				slog.String("scheduled_job_id", sj.ID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// dispatchScheduledJob resolves targets and creates individual jobs.
func (s *Scheduler) dispatchScheduledJob(ctx context.Context, sj *models.ScheduledJob, now time.Time) error {
	if sj.Target == nil {
		return fmt.Errorf("scheduled job %s has no target", sj.ID)
	}

	deviceIDs, err := s.resolveTargets(ctx, sj.TenantID, sj.Target)
	if err != nil {
		return fmt.Errorf("resolve targets: %w", err)
	}
	if len(deviceIDs) == 0 {
		s.log.Warn("scheduled job matched no devices",
			slog.String("scheduled_job_id", sj.ID))
		return nil
	}

	maxRetries := 0
	var retryJSON []byte
	if sj.RetryPolicy != nil {
		maxRetries = sj.RetryPolicy.MaxRetries
		retryJSON, _ = json.Marshal(sj.RetryPolicy)
	}

	for _, deviceID := range deviceIDs {
		jobID := models.NewJobID()
		_, err := s.pool.Exec(ctx,
			`INSERT INTO jobs (id, tenant_id, device_id, type, status, payload, retry_policy, max_retries, created_by, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			jobID, sj.TenantID, deviceID, sj.JobType, models.JobStatusQueued,
			sj.Payload, retryJSON, maxRetries, "scheduler:"+sj.ID, now,
		)
		if err != nil {
			return fmt.Errorf("create job for device %s: %w", deviceID, err)
		}
	}

	s.log.Info("scheduled job dispatched",
		slog.String("scheduled_job_id", sj.ID),
		slog.Int("device_count", len(deviceIDs)),
	)
	return nil
}

// resolveTargets expands group/tag/site IDs to device IDs, same logic as api/jobs.go.
func (s *Scheduler) resolveTargets(ctx context.Context, tenantID string, target *models.JobTarget) ([]string, error) {
	seen := make(map[string]bool)
	var deviceIDs []string

	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			deviceIDs = append(deviceIDs, id)
		}
	}

	for _, id := range target.DeviceIDs {
		add(id)
	}

	for _, groupID := range target.GroupIDs {
		rows, err := s.pool.Query(ctx,
			`SELECT dg.device_id FROM device_groups dg
			 JOIN devices d ON d.id = dg.device_id
			 WHERE dg.group_id = $1 AND d.tenant_id = $2`,
			groupID, tenantID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	for _, tagID := range target.TagIDs {
		rows, err := s.pool.Query(ctx,
			`SELECT dt.device_id FROM device_tags dt
			 JOIN devices d ON d.id = dt.device_id
			 WHERE dt.tag_id = $1 AND d.tenant_id = $2`,
			tagID, tenantID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	for _, siteID := range target.SiteIDs {
		rows, err := s.pool.Query(ctx,
			`SELECT ds.device_id FROM device_sites ds
			 JOIN devices d ON d.id = ds.device_id
			 WHERE ds.site_id = $1 AND d.tenant_id = $2`,
			siteID, tenantID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			add(id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return deviceIDs, nil
}

// AlertCondition is the parsed structure from alert_rules.condition JSONB.
type AlertCondition struct {
	Type             string            `json:"type"`
	ThresholdMinutes int               `json:"threshold_minutes"`
	Scope            *models.JobTarget `json:"scope,omitempty"`
}

func (s *Scheduler) evaluateAlertRules(ctx context.Context, now time.Time) {
	rules, err := s.store.ListEnabledAlertRules(ctx)
	if err != nil {
		s.log.Error("failed to list enabled alert rules", slog.String("error", err.Error()))
		return
	}

	for _, rule := range rules {
		var cond AlertCondition
		if err := json.Unmarshal(rule.Condition, &cond); err != nil {
			s.log.Warn("invalid alert condition JSON",
				slog.String("rule_id", rule.ID),
				slog.String("error", err.Error()),
			)
			continue
		}

		switch cond.Type {
		case "agent_offline":
			s.evaluateAgentOffline(ctx, &rule, &cond, now)
		default:
			s.log.Warn("unknown alert condition type",
				slog.String("rule_id", rule.ID),
				slog.String("type", cond.Type),
			)
		}
	}
}

func (s *Scheduler) evaluateAgentOffline(ctx context.Context, rule *models.AlertRule, cond *AlertCondition, now time.Time) {
	threshold := now.Add(-time.Duration(cond.ThresholdMinutes) * time.Minute)

	// If scope has targets, resolve to device IDs; otherwise check all devices for tenant
	var deviceIDs []string
	if cond.Scope != nil && (len(cond.Scope.DeviceIDs)+len(cond.Scope.GroupIDs)+len(cond.Scope.TagIDs)+len(cond.Scope.SiteIDs) > 0) {
		var err error
		deviceIDs, err = s.resolveTargets(ctx, rule.TenantID, cond.Scope)
		if err != nil {
			s.log.Error("failed to resolve alert scope",
				slog.String("rule_id", rule.ID),
				slog.String("error", err.Error()),
			)
			return
		}
	}

	var query string
	var args []any

	if len(deviceIDs) > 0 {
		query = `SELECT id, hostname FROM devices
				 WHERE tenant_id = $1 AND status != 'revoked' AND last_seen_at < $2
				 AND id = ANY($3)`
		args = []any{rule.TenantID, threshold, deviceIDs}
	} else {
		query = `SELECT id, hostname FROM devices
				 WHERE tenant_id = $1 AND status != 'revoked' AND last_seen_at < $2`
		args = []any{rule.TenantID, threshold}
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		s.log.Error("failed to query offline devices",
			slog.String("rule_id", rule.ID),
			slog.String("error", err.Error()),
		)
		return
	}
	defer rows.Close()

	var offlineDevices []string
	for rows.Next() {
		var id, hostname string
		if err := rows.Scan(&id, &hostname); err != nil {
			s.log.Error("failed to scan offline device", slog.String("error", err.Error()))
			continue
		}
		offlineDevices = append(offlineDevices, hostname+" ("+id+")")
	}
	if err := rows.Err(); err != nil {
		s.log.Error("failed to iterate offline devices", slog.String("error", err.Error()))
		return
	}

	if len(offlineDevices) == 0 {
		return
	}

	message := fmt.Sprintf("%d device(s) offline for >%d minutes: %s",
		len(offlineDevices), cond.ThresholdMinutes,
		truncateList(offlineDevices, 10))

	s.log.Warn("alert fired",
		slog.String("rule_id", rule.ID),
		slog.String("rule_name", rule.Name),
		slog.Int("offline_count", len(offlineDevices)),
	)

	s.notifier.Send(ctx, rule, cond.Type, message)
}

// truncateList returns at most n items joined by comma, with "..." if truncated.
func truncateList(items []string, n int) string {
	if len(items) <= n {
		result := ""
		for i, item := range items {
			if i > 0 {
				result += ", "
			}
			result += item
		}
		return result
	}
	result := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			result += ", "
		}
		result += items[i]
	}
	return result + ", ..."
}
