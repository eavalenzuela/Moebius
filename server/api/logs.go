package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/eavalenzuela/Moebius/shared/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LogsHandler handles agent log shipping (POST /v1/agents/logs).
type LogsHandler struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewLogsHandler creates a LogsHandler.
func NewLogsHandler(pool *pgxpool.Pool, log *slog.Logger) *LogsHandler {
	return &LogsHandler{pool: pool, log: log}
}

// validLogLevels is the set of accepted log levels.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// Ingest handles POST /v1/agents/logs (mTLS).
func (h *LogsHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	agentID := auth.AgentIDFromContext(r.Context())
	tenantID := auth.TenantIDFromContext(r.Context())
	if agentID == "" || tenantID == "" {
		Error(w, http.StatusUnauthorized, "agent identity not found")
		return
	}

	var req protocol.LogShipment
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Entries) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Cap at 1000 entries per request to prevent abuse
	if len(req.Entries) > 1000 {
		Error(w, http.StatusBadRequest, "too many log entries (max 1000)")
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()

	// Batch insert using a transaction
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.log.Error("begin tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store logs")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, entry := range req.Entries {
		level := entry.Level
		if !validLogLevels[level] {
			level = "info"
		}

		ts := entry.Timestamp
		if ts.IsZero() {
			ts = now
		}

		_, err := tx.Exec(ctx,
			`INSERT INTO device_logs (id, tenant_id, device_id, timestamp, level, message, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			models.NewDeviceLogID(), tenantID, agentID,
			ts, level, entry.Message, now,
		)
		if err != nil {
			h.log.Error("insert device log", slog.String("error", err.Error()))
			Error(w, http.StatusInternalServerError, "failed to store logs")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error("commit tx", slog.String("error", err.Error()))
		Error(w, http.StatusInternalServerError, "failed to store logs")
		return
	}

	h.log.Debug("logs ingested",
		slog.String("agent_id", agentID),
		slog.Int("count", len(req.Entries)),
	)

	w.WriteHeader(http.StatusNoContent)
}
