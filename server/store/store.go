package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgxpool.Pool and provides the database access layer.
// Repository methods will be added as the project grows.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store by connecting to PostgreSQL using the given DSN.
// It validates the connection before returning.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Close closes the underlying connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// Pool returns the underlying pgxpool.Pool for use in transactions
// or direct queries where the Store methods are insufficient.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
