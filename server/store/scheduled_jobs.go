package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5"
)

// CreateScheduledJob inserts a new scheduled job.
func (s *Store) CreateScheduledJob(ctx context.Context, sj *models.ScheduledJob) error {
	targetJSON, err := json.Marshal(sj.Target)
	if err != nil {
		return fmt.Errorf("marshal target: %w", err)
	}

	var retryJSON []byte
	if sj.RetryPolicy != nil {
		retryJSON, err = json.Marshal(sj.RetryPolicy)
		if err != nil {
			return fmt.Errorf("marshal retry_policy: %w", err)
		}
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO scheduled_jobs (id, tenant_id, name, job_type, payload, target, cron_expr, retry_policy, enabled, next_run_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		sj.ID, sj.TenantID, sj.Name, sj.JobType, sj.Payload, targetJSON, sj.CronExpr, retryJSON, sj.Enabled, sj.NextRunAt,
	)
	if err != nil {
		return fmt.Errorf("insert scheduled_job: %w", err)
	}
	return nil
}

// GetScheduledJob returns a single scheduled job by ID within a tenant.
func (s *Store) GetScheduledJob(ctx context.Context, tenantID, id string) (*models.ScheduledJob, error) {
	var sj models.ScheduledJob
	var targetJSON, retryJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name, job_type, payload, target, cron_expr, retry_policy, enabled, last_run_at, next_run_at
		 FROM scheduled_jobs WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&sj.ID, &sj.TenantID, &sj.Name, &sj.JobType, &sj.Payload, &targetJSON, &sj.CronExpr, &retryJSON, &sj.Enabled, &sj.LastRunAt, &sj.NextRunAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get scheduled_job: %w", err)
	}

	if targetJSON != nil {
		var t models.JobTarget
		if err := json.Unmarshal(targetJSON, &t); err == nil {
			sj.Target = &t
		}
	}
	if retryJSON != nil {
		var rp models.RetryPolicy
		if err := json.Unmarshal(retryJSON, &rp); err == nil {
			sj.RetryPolicy = &rp
		}
	}
	return &sj, nil
}

// ListScheduledJobs returns all scheduled jobs for a tenant.
func (s *Store) ListScheduledJobs(ctx context.Context, tenantID string) ([]models.ScheduledJob, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, job_type, payload, target, cron_expr, retry_policy, enabled, last_run_at, next_run_at
		 FROM scheduled_jobs WHERE tenant_id = $1 ORDER BY name`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list scheduled_jobs: %w", err)
	}
	defer rows.Close()

	var result []models.ScheduledJob
	for rows.Next() {
		var sj models.ScheduledJob
		var targetJSON, retryJSON []byte
		if err := rows.Scan(&sj.ID, &sj.TenantID, &sj.Name, &sj.JobType, &sj.Payload, &targetJSON, &sj.CronExpr, &retryJSON, &sj.Enabled, &sj.LastRunAt, &sj.NextRunAt); err != nil {
			return nil, fmt.Errorf("scan scheduled_job: %w", err)
		}
		if targetJSON != nil {
			var t models.JobTarget
			if err := json.Unmarshal(targetJSON, &t); err == nil {
				sj.Target = &t
			}
		}
		if retryJSON != nil {
			var rp models.RetryPolicy
			if err := json.Unmarshal(retryJSON, &rp); err == nil {
				sj.RetryPolicy = &rp
			}
		}
		result = append(result, sj)
	}
	return result, rows.Err()
}

// UpdateScheduledJob applies partial updates to a scheduled job.
func (s *Store) UpdateScheduledJob(ctx context.Context, tenantID, id string, updates map[string]any) error {
	// Build dynamic SET clause
	setClauses := ""
	args := []any{id, tenantID}
	idx := 3
	for k, v := range updates {
		if setClauses != "" {
			setClauses += ", "
		}
		setClauses += k + " = $" + fmt.Sprintf("%d", idx)
		args = append(args, v)
		idx++
	}
	if setClauses == "" {
		return nil
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET `+setClauses+` WHERE id = $1 AND tenant_id = $2`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("update scheduled_job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteScheduledJob removes a scheduled job.
func (s *Store) DeleteScheduledJob(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM scheduled_jobs WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("delete scheduled_job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetScheduledJobEnabled toggles the enabled state.
func (s *Store) SetScheduledJobEnabled(ctx context.Context, tenantID, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET enabled = $3 WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, enabled,
	)
	if err != nil {
		return fmt.Errorf("set scheduled_job enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListDueScheduledJobs returns enabled scheduled jobs whose next_run_at <= now, across all tenants.
// Used by the scheduler process.
func (s *Store) ListDueScheduledJobs(ctx context.Context, now time.Time) ([]models.ScheduledJob, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, job_type, payload, target, cron_expr, retry_policy, enabled, last_run_at, next_run_at
		 FROM scheduled_jobs WHERE enabled = TRUE AND next_run_at IS NOT NULL AND next_run_at <= $1
		 ORDER BY next_run_at`,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("list due scheduled_jobs: %w", err)
	}
	defer rows.Close()

	var result []models.ScheduledJob
	for rows.Next() {
		var sj models.ScheduledJob
		var targetJSON, retryJSON []byte
		if err := rows.Scan(&sj.ID, &sj.TenantID, &sj.Name, &sj.JobType, &sj.Payload, &targetJSON, &sj.CronExpr, &retryJSON, &sj.Enabled, &sj.LastRunAt, &sj.NextRunAt); err != nil {
			return nil, fmt.Errorf("scan due scheduled_job: %w", err)
		}
		if targetJSON != nil {
			var t models.JobTarget
			if err := json.Unmarshal(targetJSON, &t); err == nil {
				sj.Target = &t
			}
		}
		if retryJSON != nil {
			var rp models.RetryPolicy
			if err := json.Unmarshal(retryJSON, &rp); err == nil {
				sj.RetryPolicy = &rp
			}
		}
		result = append(result, sj)
	}
	return result, rows.Err()
}

// MarkScheduledJobRun updates last_run_at and next_run_at after execution.
func (s *Store) MarkScheduledJobRun(ctx context.Context, id string, lastRun, nextRun time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET last_run_at = $2, next_run_at = $3 WHERE id = $1`,
		id, lastRun, nextRun,
	)
	if err != nil {
		return fmt.Errorf("mark scheduled_job run: %w", err)
	}
	return nil
}
