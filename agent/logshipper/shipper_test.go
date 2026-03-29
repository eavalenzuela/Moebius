package logshipper

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/shared/protocol"
)

func TestAdd_BuffersEntries(t *testing.T) {
	s := New("http://example.com", "agent1", http.DefaultClient)

	s.Add(protocol.LogEntry{Timestamp: time.Now(), Level: "info", Message: "hello"})
	s.Add(protocol.LogEntry{Timestamp: time.Now(), Level: "error", Message: "oops"})

	if s.Pending() != 2 {
		t.Errorf("expected 2 pending, got %d", s.Pending())
	}
}

func TestAdd_DropsWhenBufferFull(t *testing.T) {
	s := New("http://example.com", "agent1", http.DefaultClient)

	for i := 0; i < defaultMaxBuffer+100; i++ {
		s.Add(protocol.LogEntry{Message: "msg"})
	}

	if s.Pending() != defaultMaxBuffer {
		t.Errorf("expected %d pending (buffer cap), got %d", defaultMaxBuffer, s.Pending())
	}
}

func TestFlush_SendsToServer(t *testing.T) {
	var received atomic.Int32
	var lastShipment protocol.LogShipment

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &lastShipment)
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "agent1", srv.Client())
	s.flushInterval = 50 * time.Millisecond

	s.Add(protocol.LogEntry{Timestamp: time.Now(), Level: "info", Message: "test1"})
	s.Add(protocol.LogEntry{Timestamp: time.Now(), Level: "warn", Message: "test2"})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	s.Run(ctx)

	if received.Load() == 0 {
		t.Fatal("expected at least one flush to server")
	}
	if s.Pending() != 0 {
		t.Errorf("expected 0 pending after flush, got %d", s.Pending())
	}
	if lastShipment.AgentID != "agent1" {
		t.Errorf("expected agent_id=agent1, got %s", lastShipment.AgentID)
	}
	if len(lastShipment.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(lastShipment.Entries))
	}
}

func TestFlush_ReBuffersOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := New(srv.URL, "agent1", srv.Client())
	s.flushInterval = 50 * time.Millisecond

	s.Add(protocol.LogEntry{Message: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	s.Run(ctx)

	// Entries should be re-buffered on server error
	if s.Pending() != 1 {
		t.Errorf("expected 1 pending (re-buffered), got %d", s.Pending())
	}
}

func TestFlush_EmptyBufferIsNoop(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := New(srv.URL, "agent1", srv.Client())
	s.flushInterval = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	s.Run(ctx)

	if calls.Load() != 0 {
		t.Errorf("expected 0 HTTP calls for empty buffer, got %d", calls.Load())
	}
}

func TestSlogHandler_TeesEntries(t *testing.T) {
	s := New("http://example.com", "agent1", http.DefaultClient)
	inner := slog.NewTextHandler(io.Discard, nil)
	h := NewHandler(s, inner, slog.LevelInfo)

	log := slog.New(h)
	log.Info("test message")
	log.Debug("should be skipped") // below minLevel
	log.Error("error message")

	if s.Pending() != 2 {
		t.Errorf("expected 2 pending (info+error, not debug), got %d", s.Pending())
	}
}

func TestSlogLevelToString(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
	}
	for _, tc := range cases {
		got := slogLevelToString(tc.level)
		if got != tc.want {
			t.Errorf("slogLevelToString(%v) = %q, want %q", tc.level, got, tc.want)
		}
	}
}
