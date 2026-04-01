package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultTokenExpiry = 24 * time.Hour

// EnrollmentService manages enrollment token lifecycle.
type EnrollmentService struct {
	pool *pgxpool.Pool
}

// NewEnrollmentService creates an EnrollmentService.
func NewEnrollmentService(pool *pgxpool.Pool) *EnrollmentService {
	return &EnrollmentService{pool: pool}
}

// CreateTokenResult is returned when a new enrollment token is created.
// The raw token is only available at creation time.
type CreateTokenResult struct {
	Token *models.EnrollmentToken
	Raw   string // plaintext token — return to operator, never stored
}

// CreateToken generates a new enrollment token. The raw token is returned
// once and never stored; only the SHA-256 hash is persisted.
func (s *EnrollmentService) CreateToken(ctx context.Context, tenantID, createdBy string, scope *models.APIScope, expiry time.Duration) (*CreateTokenResult, error) {
	if expiry == 0 {
		expiry = defaultTokenExpiry
	}

	raw, err := generateRawToken()
	if err != nil {
		return nil, err
	}
	hash := hashToken(raw)

	var scopeJSON []byte
	if scope != nil {
		scopeJSON, err = json.Marshal(scope)
		if err != nil {
			return nil, fmt.Errorf("marshal scope: %w", err)
		}
	}

	now := time.Now().UTC()
	token := &models.EnrollmentToken{
		ID:        models.NewEnrollmentTokenID(),
		TenantID:  tenantID,
		TokenHash: hash,
		CreatedBy: createdBy,
		Scope:     scope,
		ExpiresAt: now.Add(expiry),
		CreatedAt: now,
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (id, tenant_id, token_hash, created_by, scope, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		token.ID, token.TenantID, token.TokenHash, token.CreatedBy,
		scopeJSON, token.ExpiresAt, token.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert enrollment token: %w", err)
	}

	return &CreateTokenResult{Token: token, Raw: raw}, nil
}

// Peek checks that a raw token is valid (exists, not used, not expired)
// without consuming it. Used by the install script endpoint to validate
// the token before generating a script — the token is consumed later
// during actual agent enrollment.
func (s *EnrollmentService) Peek(ctx context.Context, rawToken string) (*models.EnrollmentToken, error) {
	hash := hashToken(rawToken)
	now := time.Now().UTC()

	var token models.EnrollmentToken
	var scopeJSON []byte

	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, token_hash, created_by, scope, used_at, expires_at, created_at
		 FROM enrollment_tokens
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2`,
		hash, now,
	).Scan(
		&token.ID, &token.TenantID, &token.TokenHash, &token.CreatedBy,
		&scopeJSON, &token.UsedAt, &token.ExpiresAt, &token.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("enrollment token invalid, already used, or expired")
		}
		return nil, fmt.Errorf("peek enrollment token: %w", err)
	}

	if scopeJSON != nil {
		var scope models.APIScope
		if err := json.Unmarshal(scopeJSON, &scope); err != nil {
			return nil, fmt.Errorf("unmarshal token scope: %w", err)
		}
		token.Scope = &scope
	}

	return &token, nil
}

// ValidateAndConsume checks that a raw token is valid (exists, not used,
// not expired) and atomically marks it as used. Returns the token on success.
func (s *EnrollmentService) ValidateAndConsume(ctx context.Context, rawToken string) (*models.EnrollmentToken, error) {
	hash := hashToken(rawToken)
	now := time.Now().UTC()

	var token models.EnrollmentToken
	var scopeJSON []byte

	err := s.pool.QueryRow(ctx,
		`UPDATE enrollment_tokens
		 SET used_at = $1
		 WHERE token_hash = $2 AND used_at IS NULL AND expires_at > $1
		 RETURNING id, tenant_id, token_hash, created_by, scope, used_at, expires_at, created_at`,
		now, hash,
	).Scan(
		&token.ID, &token.TenantID, &token.TokenHash, &token.CreatedBy,
		&scopeJSON, &token.UsedAt, &token.ExpiresAt, &token.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("enrollment token invalid, already used, or expired")
		}
		return nil, fmt.Errorf("validate enrollment token: %w", err)
	}

	if scopeJSON != nil {
		var scope models.APIScope
		if err := json.Unmarshal(scopeJSON, &scope); err != nil {
			return nil, fmt.Errorf("unmarshal token scope: %w", err)
		}
		token.Scope = &scope
	}

	return &token, nil
}

// generateRawToken creates a 32-byte random token encoded as hex.
func generateRawToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hashToken returns the SHA-256 hex digest of a raw token.
func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
