package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5"
)

// ListAPIKeys returns API keys for a tenant (without key hashes).
func (s *Store) ListAPIKeys(ctx context.Context, tenantID string) ([]models.APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, user_id, name, role_id, scope, is_admin, last_used_at, expires_at, created_at
		 FROM api_keys WHERE tenant_id = $1
		 ORDER BY created_at DESC`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []models.APIKey
	for rows.Next() {
		var k models.APIKey
		var scopeJSON []byte
		if err := rows.Scan(&k.ID, &k.TenantID, &k.UserID, &k.Name, &k.RoleID,
			&scopeJSON, &k.IsAdmin, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		if scopeJSON != nil {
			k.Scope = &models.APIScope{}
			_ = json.Unmarshal(scopeJSON, k.Scope)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// CreateAPIKey inserts a new API key.
func (s *Store) CreateAPIKey(ctx context.Context, k *models.APIKey) error {
	var scopeJSON []byte
	if k.Scope != nil {
		var err error
		scopeJSON, err = json.Marshal(k.Scope)
		if err != nil {
			return fmt.Errorf("marshal scope: %w", err)
		}
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, scope, is_admin, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		k.ID, k.TenantID, k.UserID, k.Name, k.KeyHash, k.RoleID,
		scopeJSON, k.IsAdmin, k.ExpiresAt, k.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create api key: %w", err)
	}
	return nil
}

// GetAPIKey returns metadata for a single API key (without the hash).
// Returns (nil, nil) if no row matches in the tenant.
func (s *Store) GetAPIKey(ctx context.Context, tenantID, keyID string) (*models.APIKey, error) {
	var k models.APIKey
	var scopeJSON []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, user_id, name, role_id, scope, is_admin, last_used_at, expires_at, created_at
		 FROM api_keys WHERE id = $1 AND tenant_id = $2`,
		keyID, tenantID,
	).Scan(&k.ID, &k.TenantID, &k.UserID, &k.Name, &k.RoleID,
		&scopeJSON, &k.IsAdmin, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	if scopeJSON != nil {
		k.Scope = &models.APIScope{}
		_ = json.Unmarshal(scopeJSON, k.Scope)
	}
	return &k, nil
}

// DeleteAPIKey revokes (deletes) an API key.
func (s *Store) DeleteAPIKey(ctx context.Context, tenantID, keyID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM api_keys WHERE id = $1 AND tenant_id = $2`,
		keyID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
