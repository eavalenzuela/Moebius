package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/eavalenzuela/Moebius/server/metrics"
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

// Log writes an audit entry to the database. On failure the error is
// returned AND logged + counted, so callers that want to surface the
// error can still do so while callers that don't care cannot silently
// drop audit writes.
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
		l.reportFailure(entry, err)
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

// LogAction is the convenience entry point used by handlers. It deliberately
// does not return an error: callers used to write `_ = h.audit.LogAction(...)`
// which silently dropped DB failures, so any compliance monitor watching for
// missing audit entries saw nothing. Now failures are logged at error level
// and counted via the `audit_write_failures_total` Prometheus counter — set
// an alert on that metric for compliance-sensitive deployments. Handlers that
// genuinely need to fail the request on audit failure should call Log directly.
func (l *Logger) LogAction(ctx context.Context, tenantID, actorID, actorType, action, resourceType, resourceID string, metadata any) {
	var raw json.RawMessage
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			l.log.Error("audit metadata marshal failed",
				slog.String("action", action),
				slog.String("tenant_id", tenantID),
				slog.String("error", err.Error()),
			)
			metrics.AuditWriteFailuresTotal.Inc()
			return
		}
		raw = b
	}

	_ = l.Log(ctx, &models.AuditEntry{
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    actorType,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Metadata:     raw,
	})
}

// reportFailure logs an audit write failure at error level and bumps the
// failure counter. Structured fields match the fields in the successful-
// write log line so ops can correlate.
func (l *Logger) reportFailure(entry *models.AuditEntry, err error) {
	l.log.Error("audit log write failed",
		slog.String("action", entry.Action),
		slog.String("actor_id", entry.ActorID),
		slog.String("resource_type", entry.ResourceType),
		slog.String("resource_id", entry.ResourceID),
		slog.String("tenant_id", entry.TenantID),
		slog.String("error", err.Error()),
	)
	metrics.AuditWriteFailuresTotal.Inc()
}
