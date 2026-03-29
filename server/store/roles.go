package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/moebius-oss/moebius/shared/models"
)

// ListRoles returns system roles (tenant_id IS NULL) and tenant custom roles.
func (s *Store) ListRoles(ctx context.Context, tenantID string) ([]models.Role, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, permissions, is_custom
		 FROM roles
		 WHERE tenant_id IS NULL OR tenant_id = $1
		 ORDER BY is_custom, name`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	var roles []models.Role
	for rows.Next() {
		var r models.Role
		var tenantNullable *string
		if err := rows.Scan(&r.ID, &tenantNullable, &r.Name, &r.Permissions, &r.IsCustom); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		if tenantNullable != nil {
			r.TenantID = *tenantNullable
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// GetRole returns a role by ID, scoped to the tenant (or system roles).
func (s *Store) GetRole(ctx context.Context, tenantID, roleID string) (*models.Role, error) {
	var r models.Role
	var tenantNullable *string
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name, permissions, is_custom
		 FROM roles
		 WHERE id = $1 AND (tenant_id IS NULL OR tenant_id = $2)`,
		roleID, tenantID,
	).Scan(&r.ID, &tenantNullable, &r.Name, &r.Permissions, &r.IsCustom)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get role: %w", err)
	}
	if tenantNullable != nil {
		r.TenantID = *tenantNullable
	}
	return &r, nil
}

// CreateRole inserts a new custom role.
func (s *Store) CreateRole(ctx context.Context, role *models.Role) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom)
		 VALUES ($1, $2, $3, $4, TRUE)`,
		role.ID, role.TenantID, role.Name, role.Permissions,
	)
	if err != nil {
		return fmt.Errorf("create role: %w", err)
	}
	return nil
}

// UpdateRole updates a custom role's name and permissions.
func (s *Store) UpdateRole(ctx context.Context, tenantID string, role *models.Role) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE roles SET name = $1, permissions = $2
		 WHERE id = $3 AND tenant_id = $4 AND is_custom = TRUE`,
		role.Name, role.Permissions, role.ID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("role not found or is a system role")
	}
	return nil
}

// DeleteRole deletes a custom role. Fails if assigned to users or API keys.
func (s *Store) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	// Check if role is assigned to any users or api keys
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM users WHERE role_id = $1) +
		        (SELECT count(*) FROM api_keys WHERE role_id = $1)`,
		roleID,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check role usage: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("role is assigned to %d users/keys and cannot be deleted", count)
	}

	tag, err := s.pool.Exec(ctx,
		`DELETE FROM roles WHERE id = $1 AND tenant_id = $2 AND is_custom = TRUE`,
		roleID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("role not found or is a system role")
	}
	return nil
}
