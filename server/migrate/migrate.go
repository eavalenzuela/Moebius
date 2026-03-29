package migrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var migrations embed.FS

// Run applies all pending up-migrations to the database.
func Run(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	// Ensure schema_migrations table exists
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Get current version
	var current int
	err = pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current)
	if err != nil {
		return fmt.Errorf("query current version: %w", err)
	}
	log.Info("current schema version", slog.Int("version", current))

	// Find and sort up-migration files
	entries, err := fs.ReadDir(migrations, "sql")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var ups []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	applied := 0
	for _, name := range ups {
		ver, err := parseVersion(name)
		if err != nil {
			return fmt.Errorf("parse migration filename %q: %w", name, err)
		}
		if ver <= current {
			continue
		}

		data, err := migrations.ReadFile("sql/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		log.Info("applying migration", slog.String("file", name), slog.Int("version", ver))

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}

		if _, err := tx.Exec(ctx, string(data)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("execute %s: %w", name, err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, ver); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}

		applied++
	}

	if applied == 0 {
		log.Info("database is up to date")
	} else {
		log.Info("migrations applied", slog.Int("count", applied))
	}
	return nil
}

func parseVersion(name string) (int, error) {
	// Expected format: 001_description.up.sql
	parts := strings.SplitN(name, "_", 2)
	if len(parts) < 2 {
		return 0, fmt.Errorf("unexpected format: %s", name)
	}
	var v int
	if _, err := fmt.Sscanf(parts[0], "%d", &v); err != nil {
		return 0, err
	}
	return v, nil
}
