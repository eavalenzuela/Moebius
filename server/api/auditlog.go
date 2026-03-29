package api

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditLogHandler serves GET /v1/audit-log.
type AuditLogHandler struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewAuditLogHandler creates an AuditLogHandler.
func NewAuditLogHandler(pool *pgxpool.Pool, log *slog.Logger) *AuditLogHandler {
	return &AuditLogHandler{pool: pool, log: log}
}

// List handles GET /v1/audit-log with cursor pagination and filters.
func (h *AuditLogHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		Error(w, http.StatusUnauthorized, "tenant not found")
		return
	}

	limit, cursorStr := ParsePagination(r)
	q := r.URL.Query()

	// Build dynamic WHERE clause
	query := `SELECT id, tenant_id, actor_id, actor_type, action, resource_type, resource_id, metadata, ip_address, created_at
		FROM audit_log WHERE tenant_id = $1`
	args := []any{tenantID}
	argN := 2

	if v := q.Get("actor_id"); v != "" {
		query += fmt.Sprintf(" AND actor_id = $%d", argN)
		args = append(args, v)
		argN++
	}
	if v := q.Get("actor_type"); v != "" {
		query += fmt.Sprintf(" AND actor_type = $%d", argN)
		args = append(args, v)
		argN++
	}
	if v := q.Get("action"); v != "" {
		query += fmt.Sprintf(" AND action = $%d", argN)
		args = append(args, v)
		argN++
	}
	if v := q.Get("resource_type"); v != "" {
		query += fmt.Sprintf(" AND resource_type = $%d", argN)
		args = append(args, v)
		argN++
	}
	if v := q.Get("resource_id"); v != "" {
		query += fmt.Sprintf(" AND resource_id = $%d", argN)
		args = append(args, v)
		argN++
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			query += fmt.Sprintf(" AND created_at >= $%d", argN)
			args = append(args, t)
			argN++
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			query += fmt.Sprintf(" AND created_at <= $%d", argN)
			args = append(args, t)
			argN++
		}
	}

	// Cursor: base64-encoded ID of last item seen
	if cursorStr != "" {
		decoded, err := base64.StdEncoding.DecodeString(cursorStr)
		if err == nil && len(decoded) > 0 {
			query += fmt.Sprintf(" AND id < $%d", argN)
			args = append(args, string(decoded))
			argN++
		}
	}

	// Order by created_at DESC (newest first), use ID as tiebreaker
	query += " ORDER BY created_at DESC, id DESC"
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit+1) // fetch one extra to detect has_more

	ctx := r.Context()
	rows, err := h.pool.Query(ctx, query, args...)
	if err != nil {
		h.log.Error("query audit log", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to query audit log")
		return
	}
	defer rows.Close()

	var entries []models.AuditEntry
	for rows.Next() {
		var e models.AuditEntry
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.ActorID, &e.ActorType,
			&e.Action, &e.ResourceType, &e.ResourceID,
			&e.Metadata, &e.IPAddress, &e.CreatedAt,
		); err != nil {
			h.log.Error("scan audit entry", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to read audit log")
			return
		}
		entries = append(entries, e)
	}

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}

	var nextCursor string
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		nextCursor = base64.StdEncoding.EncodeToString([]byte(last.ID))
	}

	if entries == nil {
		entries = []models.AuditEntry{}
	}

	JSON(w, http.StatusOK, ListResponse{
		Data: entries,
		Pagination: Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			Limit:      limit,
		},
	})
}
