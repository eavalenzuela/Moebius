package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/moebius-oss/moebius/shared/models"
)

// ListUsers returns users for a tenant with cursor-based pagination.
func (s *Store) ListUsers(ctx context.Context, tenantID, cursor string, limit int) ([]models.User, error) {
	var rows pgx.Rows
	var err error

	if cursor == "" {
		rows, err = s.pool.Query(ctx,
			`SELECT id, tenant_id, email, role_id, sso_subject, created_at
			 FROM users WHERE tenant_id = $1
			 ORDER BY created_at DESC LIMIT $2`,
			tenantID, limit+1,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT id, tenant_id, email, role_id, sso_subject, created_at
			 FROM users WHERE tenant_id = $1 AND id < $2
			 ORDER BY created_at DESC LIMIT $3`,
			tenantID, cursor, limit+1,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.RoleID, &u.SSOSubject, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// GetUser returns a user by ID, scoped to tenant.
func (s *Store) GetUser(ctx context.Context, tenantID, userID string) (*models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, email, role_id, sso_subject, created_at
		 FROM users WHERE id = $1 AND tenant_id = $2`,
		userID, tenantID,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.RoleID, &u.SSOSubject, &u.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

// CreateUser inserts a new user.
func (s *Store) CreateUser(ctx context.Context, u *models.User) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		u.ID, u.TenantID, u.Email, u.RoleID, u.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// UpdateUserRole updates a user's role assignment.
func (s *Store) UpdateUserRole(ctx context.Context, tenantID, userID, roleID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET role_id = $1 WHERE id = $2 AND tenant_id = $3`,
		roleID, userID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// DeactivateUser marks a user as deactivated by clearing their role.
func (s *Store) DeactivateUser(ctx context.Context, tenantID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET role_id = NULL WHERE id = $1 AND tenant_id = $2`,
		userID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}
