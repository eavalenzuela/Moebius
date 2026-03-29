package store

import (
	"context"
	"fmt"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ListSites(ctx context.Context, tenantID string) ([]models.Site, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, COALESCE(location, '') FROM sites WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()

	var sites []models.Site
	for rows.Next() {
		var si models.Site
		if err := rows.Scan(&si.ID, &si.TenantID, &si.Name, &si.Location); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		sites = append(sites, si)
	}
	return sites, rows.Err()
}

func (s *Store) GetSite(ctx context.Context, tenantID, siteID string) (*models.Site, error) {
	var si models.Site
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name, COALESCE(location, '') FROM sites WHERE id = $1 AND tenant_id = $2`,
		siteID, tenantID,
	).Scan(&si.ID, &si.TenantID, &si.Name, &si.Location)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get site: %w", err)
	}
	return &si, nil
}

func (s *Store) CreateSite(ctx context.Context, si *models.Site) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sites (id, tenant_id, name, location) VALUES ($1, $2, $3, $4)`,
		si.ID, si.TenantID, si.Name, si.Location)
	if err != nil {
		return fmt.Errorf("create site: %w", err)
	}
	return nil
}

func (s *Store) UpdateSite(ctx context.Context, tenantID, siteID, name, location string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sites SET name = $1, location = $2 WHERE id = $3 AND tenant_id = $4`,
		name, location, siteID, tenantID)
	if err != nil {
		return fmt.Errorf("update site: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("site not found")
	}
	return nil
}

func (s *Store) DeleteSite(ctx context.Context, tenantID, siteID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sites WHERE id = $1 AND tenant_id = $2`, siteID, tenantID)
	if err != nil {
		return fmt.Errorf("delete site: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("site not found")
	}
	return nil
}

func (s *Store) ListSiteDevices(ctx context.Context, tenantID, siteID string) ([]models.Device, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT d.id, d.tenant_id, d.hostname, d.os, d.os_version, d.arch,
				d.agent_version, d.status, d.last_seen_at, d.registered_at,
				d.cdm_enabled, d.cdm_session_active, d.cdm_session_expires_at
		 FROM devices d
		 JOIN device_sites ds ON ds.device_id = d.id
		 WHERE ds.site_id = $1 AND d.tenant_id = $2
		 ORDER BY d.hostname`, siteID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list site devices: %w", err)
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

func (s *Store) AddDevicesToSite(ctx context.Context, tenantID, siteID string, deviceIDs []string) error {
	for _, deviceID := range deviceIDs {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO device_sites (device_id, site_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			deviceID, siteID)
		if err != nil {
			return fmt.Errorf("add device %s to site: %w", deviceID, err)
		}
	}
	return nil
}

func (s *Store) RemoveDeviceFromSite(ctx context.Context, tenantID, siteID, deviceID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM device_sites WHERE device_id = $1 AND site_id = $2`, deviceID, siteID)
	if err != nil {
		return fmt.Errorf("remove device from site: %w", err)
	}
	return nil
}
