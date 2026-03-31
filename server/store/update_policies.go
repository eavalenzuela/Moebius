package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// UpsertUpdatePolicy creates or updates an agent update policy.
// A nil GroupID means the tenant-level default policy.
func (s *Store) UpsertUpdatePolicy(ctx context.Context, p *models.AgentUpdatePolicy) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_update_policies
		 (id, tenant_id, group_id, enabled, channel, schedule, rollout_strategy, rollout_batch_percent, rollout_batch_interval_minutes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (tenant_id, group_id)
		 DO UPDATE SET
		   enabled = EXCLUDED.enabled,
		   channel = EXCLUDED.channel,
		   schedule = EXCLUDED.schedule,
		   rollout_strategy = EXCLUDED.rollout_strategy,
		   rollout_batch_percent = EXCLUDED.rollout_batch_percent,
		   rollout_batch_interval_minutes = EXCLUDED.rollout_batch_interval_minutes`,
		p.ID, p.TenantID, nilIfEmpty(p.GroupID),
		p.Enabled, p.Channel, nilIfEmpty(p.Schedule),
		p.RolloutStrategy, p.RolloutBatchPercent, p.RolloutBatchIntervalMinutes,
	)
	return err
}

// GetUpdatePolicy returns a specific policy by tenant+group, or nil if not found.
func (s *Store) GetUpdatePolicy(ctx context.Context, tenantID, groupID string) (*models.AgentUpdatePolicy, error) {
	var p models.AgentUpdatePolicy
	var gid *string
	var schedule *string

	query := `SELECT id, tenant_id, group_id, enabled, channel, schedule,
	           rollout_strategy, rollout_batch_percent, rollout_batch_interval_minutes
	          FROM agent_update_policies WHERE tenant_id = $1`
	args := []any{tenantID}

	if groupID == "" {
		query += " AND group_id IS NULL"
	} else {
		query += " AND group_id = $2"
		args = append(args, groupID)
	}

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&p.ID, &p.TenantID, &gid, &p.Enabled, &p.Channel, &schedule,
		&p.RolloutStrategy, &p.RolloutBatchPercent, &p.RolloutBatchIntervalMinutes,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if gid != nil {
		p.GroupID = *gid
	}
	if schedule != nil {
		p.Schedule = *schedule
	}
	return &p, nil
}

// ListUpdatePolicies returns all policies for a tenant.
func (s *Store) ListUpdatePolicies(ctx context.Context, tenantID string) ([]models.AgentUpdatePolicy, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, group_id, enabled, channel, schedule,
		        rollout_strategy, rollout_batch_percent, rollout_batch_interval_minutes
		 FROM agent_update_policies WHERE tenant_id = $1
		 ORDER BY group_id NULLS FIRST`, tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.AgentUpdatePolicy
	for rows.Next() {
		var p models.AgentUpdatePolicy
		var gid, schedule *string
		if err := rows.Scan(&p.ID, &p.TenantID, &gid, &p.Enabled, &p.Channel, &schedule,
			&p.RolloutStrategy, &p.RolloutBatchPercent, &p.RolloutBatchIntervalMinutes); err != nil {
			return nil, err
		}
		if gid != nil {
			p.GroupID = *gid
		}
		if schedule != nil {
			p.Schedule = *schedule
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// DeleteUpdatePolicy removes a policy.
func (s *Store) DeleteUpdatePolicy(ctx context.Context, tenantID, policyID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM agent_update_policies WHERE id = $1 AND tenant_id = $2`,
		policyID, tenantID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("policy not found")
	}
	return nil
}

// GetEffectiveUpdatePolicy returns the policy for a device: group override if exists, else tenant default.
func (s *Store) GetEffectiveUpdatePolicy(ctx context.Context, tenantID string, groupIDs []string) (*models.AgentUpdatePolicy, error) {
	// Try group-level first
	if len(groupIDs) > 0 {
		for _, gid := range groupIDs {
			p, err := s.GetUpdatePolicy(ctx, tenantID, gid)
			if err != nil {
				return nil, err
			}
			if p != nil {
				return p, nil
			}
		}
	}
	// Fall back to tenant default
	return s.GetUpdatePolicy(ctx, tenantID, "")
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
