package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5"
)

// CreateAlertRule inserts a new alert rule.
func (s *Store) CreateAlertRule(ctx context.Context, ar *models.AlertRule) error {
	channelsJSON, err := json.Marshal(ar.Channels)
	if err != nil {
		return fmt.Errorf("marshal channels: %w", err)
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO alert_rules (id, tenant_id, name, condition, channels, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		ar.ID, ar.TenantID, ar.Name, ar.Condition, channelsJSON, ar.Enabled,
	)
	if err != nil {
		return fmt.Errorf("insert alert_rule: %w", err)
	}
	return nil
}

// GetAlertRule returns a single alert rule by ID within a tenant.
func (s *Store) GetAlertRule(ctx context.Context, tenantID, id string) (*models.AlertRule, error) {
	var ar models.AlertRule
	var channelsJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name, condition, channels, enabled
		 FROM alert_rules WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&ar.ID, &ar.TenantID, &ar.Name, &ar.Condition, &channelsJSON, &ar.Enabled)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get alert_rule: %w", err)
	}

	if channelsJSON != nil {
		var ch models.AlertChannels
		if err := json.Unmarshal(channelsJSON, &ch); err == nil {
			ar.Channels = &ch
		}
	}
	return &ar, nil
}

// ListAlertRules returns all alert rules for a tenant.
func (s *Store) ListAlertRules(ctx context.Context, tenantID string) ([]models.AlertRule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, condition, channels, enabled
		 FROM alert_rules WHERE tenant_id = $1 ORDER BY name`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list alert_rules: %w", err)
	}
	defer rows.Close()

	var result []models.AlertRule
	for rows.Next() {
		var ar models.AlertRule
		var channelsJSON []byte
		if err := rows.Scan(&ar.ID, &ar.TenantID, &ar.Name, &ar.Condition, &channelsJSON, &ar.Enabled); err != nil {
			return nil, fmt.Errorf("scan alert_rule: %w", err)
		}
		if channelsJSON != nil {
			var ch models.AlertChannels
			if err := json.Unmarshal(channelsJSON, &ch); err == nil {
				ar.Channels = &ch
			}
		}
		result = append(result, ar)
	}
	return result, rows.Err()
}

// UpdateAlertRule applies partial updates to an alert rule.
func (s *Store) UpdateAlertRule(ctx context.Context, tenantID, id string, updates map[string]any) error {
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
		`UPDATE alert_rules SET `+setClauses+` WHERE id = $1 AND tenant_id = $2`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("update alert_rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteAlertRule removes an alert rule.
func (s *Store) DeleteAlertRule(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM alert_rules WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("delete alert_rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetAlertRuleEnabled toggles the enabled state.
func (s *Store) SetAlertRuleEnabled(ctx context.Context, tenantID, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE alert_rules SET enabled = $3 WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, enabled,
	)
	if err != nil {
		return fmt.Errorf("set alert_rule enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListEnabledAlertRules returns all enabled alert rules across all tenants.
// Used by the scheduler process.
func (s *Store) ListEnabledAlertRules(ctx context.Context) ([]models.AlertRule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, condition, channels, enabled
		 FROM alert_rules WHERE enabled = TRUE`,
	)
	if err != nil {
		return nil, fmt.Errorf("list enabled alert_rules: %w", err)
	}
	defer rows.Close()

	var result []models.AlertRule
	for rows.Next() {
		var ar models.AlertRule
		var channelsJSON []byte
		if err := rows.Scan(&ar.ID, &ar.TenantID, &ar.Name, &ar.Condition, &channelsJSON, &ar.Enabled); err != nil {
			return nil, fmt.Errorf("scan alert_rule: %w", err)
		}
		if channelsJSON != nil {
			var ch models.AlertChannels
			if err := json.Unmarshal(channelsJSON, &ch); err == nil {
				ar.Channels = &ch
			}
		}
		result = append(result, ar)
	}
	return result, rows.Err()
}
