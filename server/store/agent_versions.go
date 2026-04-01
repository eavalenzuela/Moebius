package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// CreateAgentVersion inserts a version row and its binaries in a transaction.
func (s *Store) CreateAgentVersion(ctx context.Context, v *models.AgentVersion) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is a no-op after commit

	_, err = tx.Exec(ctx,
		`INSERT INTO agent_versions (id, version, channel, changelog, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		v.ID, v.Version, v.Channel, v.Changelog, v.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert agent_versions: %w", err)
	}

	for i := range v.Binaries {
		b := &v.Binaries[i]
		_, err = tx.Exec(ctx,
			`INSERT INTO agent_version_binaries (id, agent_version_id, os, arch, file_id, sha256, signature, signature_key_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			b.ID, v.ID, b.OS, b.Arch, b.FileID, b.SHA256, b.Signature, b.SignatureKeyID,
		)
		if err != nil {
			return fmt.Errorf("insert binary %s/%s: %w", b.OS, b.Arch, err)
		}
	}

	return tx.Commit(ctx)
}

// GetAgentVersion returns a version with its binaries, or nil if not found.
func (s *Store) GetAgentVersion(ctx context.Context, version string) (*models.AgentVersion, error) {
	var v models.AgentVersion
	err := s.pool.QueryRow(ctx,
		`SELECT id, version, channel, changelog, yanked, yank_reason, created_at
		 FROM agent_versions WHERE version = $1`, version,
	).Scan(&v.ID, &v.Version, &v.Channel, &v.Changelog, &v.Yanked, &v.YankReason, &v.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	binaries, err := s.getVersionBinaries(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	v.Binaries = binaries
	return &v, nil
}

// GetAgentVersionByID returns a version by its primary key.
func (s *Store) GetAgentVersionByID(ctx context.Context, id string) (*models.AgentVersion, error) {
	var v models.AgentVersion
	err := s.pool.QueryRow(ctx,
		`SELECT id, version, channel, changelog, yanked, yank_reason, created_at
		 FROM agent_versions WHERE id = $1`, id,
	).Scan(&v.ID, &v.Version, &v.Channel, &v.Changelog, &v.Yanked, &v.YankReason, &v.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	binaries, err := s.getVersionBinaries(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	v.Binaries = binaries
	return &v, nil
}

// ListAgentVersions returns versions with cursor pagination, optionally filtered by channel.
func (s *Store) ListAgentVersions(ctx context.Context, channel, cursor string, limit int) ([]models.AgentVersion, error) {
	query := `SELECT id, version, channel, changelog, yanked, yank_reason, created_at
	          FROM agent_versions`
	args := []any{}
	argIdx := 1

	var where string
	if channel != "" {
		where = fmt.Sprintf(" WHERE channel = $%d", argIdx)
		args = append(args, channel)
		argIdx++
	}
	if cursor != "" {
		if where == "" {
			where = fmt.Sprintf(" WHERE id < $%d", argIdx)
		} else {
			where += fmt.Sprintf(" AND id < $%d", argIdx)
		}
		args = append(args, cursor)
		argIdx++
	}

	query += where + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.AgentVersion
	for rows.Next() {
		var v models.AgentVersion
		if err := rows.Scan(&v.ID, &v.Version, &v.Channel, &v.Changelog, &v.Yanked, &v.YankReason, &v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load binaries for each version
	for i := range result {
		binaries, err := s.getVersionBinaries(ctx, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].Binaries = binaries
	}
	return result, nil
}

// YankAgentVersion marks a version as yanked.
func (s *Store) YankAgentVersion(ctx context.Context, version, reason string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_versions SET yanked = TRUE, yank_reason = $1 WHERE version = $2`,
		reason, version,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("version %q not found", version)
	}
	return nil
}

// GetLatestAgentVersion returns the latest non-yanked version on a channel.
func (s *Store) GetLatestAgentVersion(ctx context.Context, channel string) (*models.AgentVersion, error) {
	var v models.AgentVersion
	err := s.pool.QueryRow(ctx,
		`SELECT id, version, channel, changelog, yanked, yank_reason, created_at
		 FROM agent_versions
		 WHERE channel = $1 AND yanked = FALSE
		 ORDER BY created_at DESC
		 LIMIT 1`, channel,
	).Scan(&v.ID, &v.Version, &v.Channel, &v.Changelog, &v.Yanked, &v.YankReason, &v.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	binaries, err := s.getVersionBinaries(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	v.Binaries = binaries
	return &v, nil
}

func (s *Store) getVersionBinaries(ctx context.Context, versionID string) ([]models.AgentVersionBinary, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, agent_version_id, os, arch, file_id, sha256, signature, signature_key_id
		 FROM agent_version_binaries WHERE agent_version_id = $1`, versionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.AgentVersionBinary
	for rows.Next() {
		var b models.AgentVersionBinary
		if err := rows.Scan(&b.ID, &b.AgentVersionID, &b.OS, &b.Arch, &b.FileID, &b.SHA256, &b.Signature, &b.SignatureKeyID); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}
