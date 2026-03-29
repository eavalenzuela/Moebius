package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/moebius-oss/moebius/shared/models"
)

// GetTenant returns a tenant by ID.
func (s *Store) GetTenant(ctx context.Context, tenantID string) (*models.Tenant, error) {
	var t models.Tenant
	var configJSON []byte

	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, config, created_at
		 FROM tenants WHERE id = $1`,
		tenantID,
	).Scan(&t.ID, &t.Name, &t.Slug, &configJSON, &t.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	if configJSON != nil {
		t.Config = &models.TenantConfig{}
		_ = json.Unmarshal(configJSON, t.Config)
	}

	return &t, nil
}

// UpdateTenant updates a tenant's name and config.
func (s *Store) UpdateTenant(ctx context.Context, t *models.Tenant) error {
	var configJSON []byte
	if t.Config != nil {
		configJSON, _ = json.Marshal(t.Config)
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants SET name = $1, config = $2 WHERE id = $3`,
		t.Name, configJSON, t.ID,
	)
	if err != nil {
		return fmt.Errorf("update tenant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tenant not found")
	}
	return nil
}
