package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// CreateRollout inserts a new rollout record.
func (s *Store) CreateRollout(ctx context.Context, r *models.Rollout) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_rollouts
		 (id, agent_version_id, tenant_id, status, strategy, batch_percent,
		  batch_interval_minutes, total_devices, seed, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		r.ID, r.AgentVersionID, r.TenantID, r.Status, r.Strategy,
		r.BatchPercent, r.BatchIntervalMinutes, r.TotalDevices, r.Seed, r.CreatedAt,
	)
	return err
}

// GetRollout returns a rollout by agent version and tenant.
func (s *Store) GetRollout(ctx context.Context, agentVersionID, tenantID string) (*models.Rollout, error) {
	var r models.Rollout
	err := s.pool.QueryRow(ctx,
		`SELECT id, agent_version_id, tenant_id, status, strategy, batch_percent,
		        batch_interval_minutes, current_batch, total_devices, updated_devices,
		        rolled_back_devices, seed, last_batch_at, created_at
		 FROM agent_rollouts WHERE agent_version_id = $1 AND tenant_id = $2`,
		agentVersionID, tenantID,
	).Scan(
		&r.ID, &r.AgentVersionID, &r.TenantID, &r.Status, &r.Strategy,
		&r.BatchPercent, &r.BatchIntervalMinutes, &r.CurrentBatch, &r.TotalDevices,
		&r.UpdatedDevices, &r.RolledBackDevices, &r.Seed, &r.LastBatchAt, &r.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// GetRolloutByID returns a rollout by its primary key.
func (s *Store) GetRolloutByID(ctx context.Context, id string) (*models.Rollout, error) {
	var r models.Rollout
	err := s.pool.QueryRow(ctx,
		`SELECT id, agent_version_id, tenant_id, status, strategy, batch_percent,
		        batch_interval_minutes, current_batch, total_devices, updated_devices,
		        rolled_back_devices, seed, last_batch_at, created_at
		 FROM agent_rollouts WHERE id = $1`, id,
	).Scan(
		&r.ID, &r.AgentVersionID, &r.TenantID, &r.Status, &r.Strategy,
		&r.BatchPercent, &r.BatchIntervalMinutes, &r.CurrentBatch, &r.TotalDevices,
		&r.UpdatedDevices, &r.RolledBackDevices, &r.Seed, &r.LastBatchAt, &r.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// UpdateRolloutStatus updates the status of a rollout.
func (s *Store) UpdateRolloutStatus(ctx context.Context, id, status string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_rollouts SET status = $1 WHERE id = $2`, status, id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("rollout not found")
	}
	return nil
}

// UpdateRolloutBatch increments the batch counters after a batch is dispatched.
func (s *Store) UpdateRolloutBatch(ctx context.Context, id string, batch, updatedDevices int, lastBatchAt interface{}) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE agent_rollouts SET current_batch = $1, updated_devices = $2, last_batch_at = $3 WHERE id = $4`,
		batch, updatedDevices, lastBatchAt, id,
	)
	return err
}

// IncrementRollbackCount increments the rolled_back_devices counter.
func (s *Store) IncrementRollbackCount(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE agent_rollouts SET rolled_back_devices = rolled_back_devices + 1 WHERE id = $1`, id,
	)
	return err
}

// ListActiveRollouts returns all in-progress rollouts for the scheduler to evaluate.
func (s *Store) ListActiveRollouts(ctx context.Context) ([]models.Rollout, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, agent_version_id, tenant_id, status, strategy, batch_percent,
		        batch_interval_minutes, current_batch, total_devices, updated_devices,
		        rolled_back_devices, seed, last_batch_at, created_at
		 FROM agent_rollouts WHERE status = $1`,
		models.RolloutStatusInProgress,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.Rollout
	for rows.Next() {
		var r models.Rollout
		if err := rows.Scan(
			&r.ID, &r.AgentVersionID, &r.TenantID, &r.Status, &r.Strategy,
			&r.BatchPercent, &r.BatchIntervalMinutes, &r.CurrentBatch, &r.TotalDevices,
			&r.UpdatedDevices, &r.RolledBackDevices, &r.Seed, &r.LastBatchAt, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
