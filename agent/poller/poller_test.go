package poller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/moebius-oss/moebius/shared/protocol"
)

func TestPoller_CheckinAndDispatch(t *testing.T) {
	var receivedReq protocol.CheckinRequest
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		resp := protocol.CheckinResponse{
			Timestamp: time.Now(),
			Jobs: []protocol.JobDispatch{
				{JobID: "job_1", Type: "exec", CreatedAt: time.Now()},
				{JobID: "job_2", Type: "inventory_full", CreatedAt: time.Now()},
			},
			Config: &protocol.AgentConfig{PollIntervalSeconds: 15},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	var jobsReceived []string
	var jobsMu sync.Mutex

	p := New(Config{
		ServerURL:    server.URL,
		AgentID:      "agt_test123",
		PollInterval: 30,
		Client:       server.Client(),
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		JobHandler: func(job protocol.JobDispatch) {
			jobsMu.Lock()
			jobsReceived = append(jobsReceived, job.JobID)
			jobsMu.Unlock()
		},
	})

	// Run one check-in cycle via Run (cancel after first tick)
	ctx, cancel := context.WithCancel(context.Background())

	// Run in background, cancel after a short time
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_ = p.Run(ctx)

	// Verify request
	mu.Lock()
	if receivedReq.AgentID != "agt_test123" {
		t.Errorf("AgentID = %q, want %q", receivedReq.AgentID, "agt_test123")
	}
	if receivedReq.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", receivedReq.Sequence)
	}
	mu.Unlock()

	// Verify jobs dispatched
	jobsMu.Lock()
	if len(jobsReceived) != 2 {
		t.Errorf("received %d jobs, want 2", len(jobsReceived))
	}
	jobsMu.Unlock()

	// Verify poll interval updated
	if p.PollInterval() != 15 {
		t.Errorf("PollInterval = %d, want 15", p.PollInterval())
	}
}

func TestPoller_SequenceIncrements(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		resp := protocol.CheckinResponse{Timestamp: time.Now()}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(Config{
		ServerURL:    server.URL,
		AgentID:      "agt_seq",
		PollInterval: 1, // 1 second for fast test
		Client:       server.Client(),
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Let it run for ~1.5 seconds (initial + 1 tick)
		time.Sleep(1500 * time.Millisecond)
		cancel()
	}()
	_ = p.Run(ctx)

	if p.Sequence() < 2 {
		t.Errorf("Sequence = %d, want >= 2", p.Sequence())
	}
}

func TestPoller_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	p := New(Config{
		ServerURL:    server.URL,
		AgentID:      "agt_err",
		PollInterval: 30,
		Client:       server.Client(),
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	// Should not panic on server error
	_ = p.Run(ctx)

	// Sequence should still have incremented
	if p.Sequence() != 1 {
		t.Errorf("Sequence = %d, want 1", p.Sequence())
	}
}

func TestPoller_SetCDMState(t *testing.T) {
	var receivedReq protocol.CheckinRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedReq)
		resp := protocol.CheckinResponse{Timestamp: time.Now()}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := New(Config{
		ServerURL:    server.URL,
		AgentID:      "agt_cdm",
		PollInterval: 30,
		Client:       server.Client(),
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	expiry := time.Now().Add(10 * time.Minute)
	p.SetCDMState(true, true, &expiry)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_ = p.Run(ctx)

	if !receivedReq.Status.CDMEnabled {
		t.Error("CDMEnabled should be true")
	}
	if !receivedReq.Status.CDMSessionActive {
		t.Error("CDMSessionActive should be true")
	}
	if receivedReq.Status.CDMSessionExpiresAt == nil {
		t.Error("CDMSessionExpiresAt should not be nil")
	}
}

func TestReadAgentID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent_id")
	_ = os.WriteFile(path, []byte("agt_abc123\n"), 0o600)

	id, err := ReadAgentID(path)
	if err != nil {
		t.Fatalf("ReadAgentID: %v", err)
	}
	if id != "agt_abc123" {
		t.Errorf("id = %q, want %q", id, "agt_abc123")
	}
}

func TestReadAgentID_Missing(t *testing.T) {
	_, err := ReadAgentID("/nonexistent/agent_id")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadAgentID_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent_id")
	_ = os.WriteFile(path, []byte("  \n"), 0o600)

	_, err := ReadAgentID(path)
	if err == nil {
		t.Fatal("expected error for empty agent_id")
	}
}
