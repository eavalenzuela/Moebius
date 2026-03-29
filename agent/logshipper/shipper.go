// Package logshipper buffers agent log entries and periodically ships them
// to the server via POST /v1/agents/logs. Log shipping is not gated by CDM.
package logshipper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

const (
	defaultFlushInterval = 10 * time.Second
	defaultMaxBatch      = 500
	defaultMaxBuffer     = 5000
)

// Shipper buffers log entries and periodically ships them to the server.
type Shipper struct {
	serverURL string
	agentID   string
	client    *http.Client

	flushInterval time.Duration
	maxBatch      int

	mu     sync.Mutex
	buffer []protocol.LogEntry
}

// New creates a Shipper.
func New(serverURL, agentID string, client *http.Client) *Shipper {
	return &Shipper{
		serverURL:     strings.TrimRight(serverURL, "/"),
		agentID:       agentID,
		client:        client,
		flushInterval: defaultFlushInterval,
		maxBatch:      defaultMaxBatch,
	}
}

// Add enqueues a log entry for shipping. Safe for concurrent use.
// Drops entries silently if the buffer is full to avoid backpressure.
func (s *Shipper) Add(entry protocol.LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buffer) >= defaultMaxBuffer {
		return // drop to avoid unbounded memory
	}
	s.buffer = append(s.buffer, entry)
}

// Run starts the flush loop. It blocks until ctx is cancelled.
// On shutdown, it makes one final flush attempt.
func (s *Shipper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flush(context.Background()) // final flush
			return
		case <-ticker.C:
			s.flush(ctx)
		}
	}
}

// flush sends buffered entries to the server and clears the buffer.
func (s *Shipper) flush(ctx context.Context) {
	s.mu.Lock()
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return
	}

	// Take up to maxBatch entries
	n := len(s.buffer)
	if n > s.maxBatch {
		n = s.maxBatch
	}
	batch := make([]protocol.LogEntry, n)
	copy(batch, s.buffer[:n])
	s.buffer = s.buffer[n:]
	s.mu.Unlock()

	shipment := protocol.LogShipment{
		AgentID: s.agentID,
		Entries: batch,
	}

	body, err := json.Marshal(shipment)
	if err != nil {
		return
	}

	url := s.serverURL + "/v1/agents/logs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// Re-buffer on failure so entries aren't lost
		s.mu.Lock()
		s.buffer = append(batch, s.buffer...)
		if len(s.buffer) > defaultMaxBuffer {
			s.buffer = s.buffer[:defaultMaxBuffer]
		}
		s.mu.Unlock()
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		// Server error — re-buffer
		s.mu.Lock()
		s.buffer = append(batch, s.buffer...)
		if len(s.buffer) > defaultMaxBuffer {
			s.buffer = s.buffer[:defaultMaxBuffer]
		}
		s.mu.Unlock()
	}
}

// Pending returns the number of buffered entries.
func (s *Shipper) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buffer)
}

// Handler returns an slog.Handler that writes to this Shipper and to
// an underlying handler (typically the existing stderr handler).
type Handler struct {
	shipper  *Shipper
	inner    slog.Handler
	minLevel slog.Level
}

// NewHandler creates a tee handler: log records go to both the inner handler
// and the shipper buffer.
func NewHandler(shipper *Shipper, inner slog.Handler, minLevel slog.Level) *Handler {
	return &Handler{shipper: shipper, inner: inner, minLevel: minLevel}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	// Ship if at or above min level
	if r.Level >= h.minLevel {
		h.shipper.Add(protocol.LogEntry{
			Timestamp: r.Time.UTC(),
			Level:     slogLevelToString(r.Level),
			Message:   r.Message,
		})
	}
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{
		shipper:  h.shipper,
		inner:    h.inner.WithAttrs(attrs),
		minLevel: h.minLevel,
	}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		shipper:  h.shipper,
		inner:    h.inner.WithGroup(name),
		minLevel: h.minLevel,
	}
}

func slogLevelToString(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}
