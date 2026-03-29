package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/moebius-oss/moebius/shared/models"
)

func (s *Store) ListGroups(ctx context.Context, tenantID string) ([]models.Group, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name FROM groups WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.TenantID, &g.Name); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Store) GetGroup(ctx context.Context, tenantID, groupID string) (*models.Group, error) {
	var g models.Group
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name FROM groups WHERE id = $1 AND tenant_id = $2`,
		groupID, tenantID,
	).Scan(&g.ID, &g.TenantID, &g.Name)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get group: %w", err)
	}
	return &g, nil
}

func (s *Store) CreateGroup(ctx context.Context, g *models.Group) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO groups (id, tenant_id, name) VALUES ($1, $2, $3)`,
		g.ID, g.TenantID, g.Name)
	if err != nil {
		return fmt.Errorf("create group: %w", err)
	}
	return nil
}

func (s *Store) UpdateGroup(ctx context.Context, tenantID, groupID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE groups SET name = $1 WHERE id = $2 AND tenant_id = $3`,
		name, groupID, tenantID)
	if err != nil {
		return fmt.Errorf("update group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("group not found")
	}
	return nil
}

func (s *Store) DeleteGroup(ctx context.Context, tenantID, groupID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM groups WHERE id = $1 AND tenant_id = $2`, groupID, tenantID)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("group not found")
	}
	return nil
}

func (s *Store) ListGroupDevices(ctx context.Context, tenantID, groupID string) ([]models.Device, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT d.id, d.tenant_id, d.hostname, d.os, d.os_version, d.arch,
				d.agent_version, d.status, d.last_seen_at, d.registered_at,
				d.cdm_enabled, d.cdm_session_active, d.cdm_session_expires_at
		 FROM devices d
		 JOIN device_groups dg ON dg.device_id = d.id
		 WHERE dg.group_id = $1 AND d.tenant_id = $2
		 ORDER BY d.hostname`, groupID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list group devices: %w", err)
	}
	defer rows.Close()

	var devices []models.Device
	for rows.Next() {
		var d models.Device
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Hostname, &d.OS, &d.OSVersion, &d.Arch,
			&d.AgentVersion, &d.Status, &d.LastSeenAt, &d.RegisteredAt,
			&d.CDMEnabled, &d.CDMSessionActive, &d.CDMSessionExpiresAt); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (s *Store) AddDevicesToGroup(ctx context.Context, tenantID, groupID string, deviceIDs []string) error {
	for _, deviceID := range deviceIDs {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO device_groups (device_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			deviceID, groupID)
		if err != nil {
			return fmt.Errorf("add device %s to group: %w", deviceID, err)
		}
	}
	return nil
}

func (s *Store) RemoveDeviceFromGroup(ctx context.Context, tenantID, groupID, deviceID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM device_groups WHERE device_id = $1 AND group_id = $2`, deviceID, groupID)
	if err != nil {
		return fmt.Errorf("remove device from group: %w", err)
	}
	return nil
}
