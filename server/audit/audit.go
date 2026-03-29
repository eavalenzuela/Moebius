package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Logger writes audit entries to the audit_log table.
type Logger struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// New creates an audit Logger.
func New(pool *pgxpool.Pool, log *slog.Logger) *Logger {
	return &Logger{pool: pool, log: log}
}

// Log writes an audit entry to the database.
func (l *Logger) Log(ctx context.Context, entry *models.AuditEntry) error {
	entry.ID = models.NewAuditEntryID()
	entry.CreatedAt = time.Now().UTC()

	_, err := l.pool.Exec(ctx,
		`INSERT INTO audit_log (id, tenant_id, actor_id, actor_type, action, resource_type, resource_id, metadata, ip_address, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entry.ID, entry.TenantID, entry.ActorID, entry.ActorType,
		entry.Action, entry.ResourceType, entry.ResourceID,
		entry.Metadata, entry.IPAddress, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}

	l.log.Info("audit",
		slog.String("action", entry.Action),
		slog.String("actor_id", entry.ActorID),
		slog.String("resource_type", entry.ResourceType),
		slog.String("resource_id", entry.ResourceID),
		slog.String("tenant_id", entry.TenantID),
	)
	return nil
}

// LogAction is a convenience method for the common case.
func (l *Logger) LogAction(ctx context.Context, tenantID, actorID, actorType, action, resourceType, resourceID string, metadata any) error {
	var raw json.RawMessage
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal audit metadata: %w", err)
		}
		raw = b
	}

	return l.Log(ctx, &models.AuditEntry{
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    actorType,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Metadata:     raw,
	})
}
