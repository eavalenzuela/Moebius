package store

import (
	"context"
	"fmt"

	"github.com/moebius-oss/moebius/shared/models"
)

func (s *Store) ListTags(ctx context.Context, tenantID string) ([]models.Tag, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name FROM tags WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()

	var tags []models.Tag
	for rows.Next() {
		var t models.Tag
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

func (s *Store) CreateTag(ctx context.Context, t *models.Tag) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tags (id, tenant_id, name) VALUES ($1, $2, $3)`,
		t.ID, t.TenantID, t.Name)
	if err != nil {
		return fmt.Errorf("create tag: %w", err)
	}
	return nil
}

func (s *Store) DeleteTag(ctx context.Context, tenantID, tagID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tags WHERE id = $1 AND tenant_id = $2`, tagID, tenantID)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tag not found")
	}
	return nil
}

func (s *Store) AddTagsToDevice(ctx context.Context, tenantID, deviceID string, tagIDs []string) error {
	for _, tagID := range tagIDs {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO device_tags (device_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			deviceID, tagID)
		if err != nil {
			return fmt.Errorf("add tag %s to device: %w", tagID, err)
		}
	}
	return nil
}

func (s *Store) RemoveTagFromDevice(ctx context.Context, tenantID, deviceID, tagID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM device_tags WHERE device_id = $1 AND tag_id = $2`, deviceID, tagID)
	if err != nil {
		return fmt.Errorf("remove tag from device: %w", err)
	}
	return nil
}
