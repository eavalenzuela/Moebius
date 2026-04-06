package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5"
)

// DeviceFilters holds optional filters for listing devices.
type DeviceFilters struct {
	Status         string
	GroupID        string
	TagID          string
	SiteID         string
	OS             string
	Search         string
	ScopeDeviceIDs []string // when non-empty, restricts results to these device IDs (scope enforcement)
}

// ListDevices returns devices for a tenant with optional filters.
func (s *Store) ListDevices(ctx context.Context, tenantID string, f DeviceFilters) ([]models.Device, error) {
	query := `SELECT d.id, d.tenant_id, d.hostname, d.os, d.os_version, d.arch,
					 d.agent_version, d.status, d.last_seen_at, d.registered_at,
					 d.cdm_enabled, d.cdm_session_active, d.cdm_session_expires_at
			  FROM devices d
			  WHERE d.tenant_id = $1`
	args := []any{tenantID}
	idx := 2

	if len(f.ScopeDeviceIDs) > 0 {
		query += " AND d.id = ANY($" + strconv.Itoa(idx) + ")"
		args = append(args, f.ScopeDeviceIDs)
		idx++
	}
	if f.GroupID != "" {
		query += " AND d.id IN (SELECT device_id FROM device_groups WHERE group_id = $" + strconv.Itoa(idx) + ")"
		args = append(args, f.GroupID)
		idx++
	}
	if f.TagID != "" {
		query += " AND d.id IN (SELECT device_id FROM device_tags WHERE tag_id = $" + strconv.Itoa(idx) + ")"
		args = append(args, f.TagID)
		idx++
	}
	if f.SiteID != "" {
		query += " AND d.id IN (SELECT device_id FROM device_sites WHERE site_id = $" + strconv.Itoa(idx) + ")"
		args = append(args, f.SiteID)
		idx++
	}
	if f.OS != "" {
		query += " AND d.os = $" + strconv.Itoa(idx)
		args = append(args, f.OS)
		idx++
	}
	if f.Search != "" {
		query += " AND d.hostname ILIKE $" + strconv.Itoa(idx)
		args = append(args, "%"+f.Search+"%")
		idx++
	}
	if f.Status != "" {
		query += " AND d.status = $" + strconv.Itoa(idx)
		args = append(args, f.Status)
		idx++
	}
	_ = idx

	query += " ORDER BY d.registered_at DESC LIMIT 100"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
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

// GetDevice returns a single device with its groups, tags, and sites populated.
func (s *Store) GetDevice(ctx context.Context, tenantID, deviceID string) (*models.Device, error) {
	var d models.Device
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, hostname, os, os_version, arch, agent_version,
				status, last_seen_at, registered_at, cdm_enabled,
				cdm_session_active, cdm_session_expires_at, sequence_last
		 FROM devices WHERE id = $1 AND tenant_id = $2`,
		deviceID, tenantID,
	).Scan(&d.ID, &d.TenantID, &d.Hostname, &d.OS, &d.OSVersion, &d.Arch, &d.AgentVersion,
		&d.Status, &d.LastSeenAt, &d.RegisteredAt, &d.CDMEnabled,
		&d.CDMSessionActive, &d.CDMSessionExpiresAt, &d.SequenceLast)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get device: %w", err)
	}

	// Populate groups
	rows, err := s.pool.Query(ctx,
		`SELECT g.name FROM groups g JOIN device_groups dg ON dg.group_id = g.id WHERE dg.device_id = $1`, deviceID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				d.Groups = append(d.Groups, name)
			}
		}
	}

	// Populate tags
	rows2, err := s.pool.Query(ctx,
		`SELECT t.name FROM tags t JOIN device_tags dt ON dt.tag_id = t.id WHERE dt.device_id = $1`, deviceID)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var name string
			if err := rows2.Scan(&name); err == nil {
				d.Tags = append(d.Tags, name)
			}
		}
	}

	// Populate sites
	rows3, err := s.pool.Query(ctx,
		`SELECT s.name FROM sites s JOIN device_sites ds ON ds.site_id = s.id WHERE ds.device_id = $1`, deviceID)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var name string
			if err := rows3.Scan(&name); err == nil {
				d.Sites = append(d.Sites, name)
			}
		}
	}

	return &d, nil
}

// UpdateDevice updates a device's mutable fields (hostname).
func (s *Store) UpdateDevice(ctx context.Context, tenantID, deviceID, hostname string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE devices SET hostname = $1 WHERE id = $2 AND tenant_id = $3`,
		hostname, deviceID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("device not found")
	}
	return nil
}

// RevokeDevice marks a device as revoked, revokes all its certificates,
// and cancels all pending jobs. Runs in a transaction.
func (s *Store) RevokeDevice(ctx context.Context, tenantID, deviceID, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on commit is a no-op

	now := time.Now().UTC()

	// Mark device as revoked
	tag, err := tx.Exec(ctx,
		`UPDATE devices SET status = $1 WHERE id = $2 AND tenant_id = $3`,
		models.DeviceStatusRevoked, deviceID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("device not found")
	}

	// Revoke all active certificates for this device
	_, err = tx.Exec(ctx,
		`UPDATE agent_certificates SET revoked_at = $1, revocation_reason = $2
		 WHERE device_id = $3 AND revoked_at IS NULL`,
		now, reason, deviceID,
	)
	if err != nil {
		return fmt.Errorf("revoke certificates: %w", err)
	}

	// Cancel all pending/queued jobs for this device
	_, err = tx.Exec(ctx,
		`UPDATE jobs SET status = $1
		 WHERE device_id = $2 AND tenant_id = $3
		 AND status IN ($4, $5, $6)`,
		models.JobStatusCancelled, deviceID, tenantID,
		models.JobStatusPending, models.JobStatusQueued, models.JobStatusDispatched,
	)
	if err != nil {
		return fmt.Errorf("cancel pending jobs: %w", err)
	}

	return tx.Commit(ctx)
}
