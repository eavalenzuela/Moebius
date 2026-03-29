package store

import (
	"context"
	"fmt"
	"time"

	"github.com/moebius-oss/moebius/shared/models"
)

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
