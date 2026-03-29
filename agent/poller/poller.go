package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moebius-oss/moebius/shared/protocol"
	"github.com/moebius-oss/moebius/shared/version"
)

// JobHandler is called for each job received in a check-in response.
type JobHandler func(job protocol.JobDispatch)

// Poller polls the server at a configurable interval and dispatches jobs.
type Poller struct {
	serverURL string
	agentID   string
	client    *http.Client
	log       *slog.Logger

	pollInterval atomic.Int64 // seconds, updated by server config
	sequence     atomic.Int64
	startTime    time.Time

	jobHandler JobHandler

	// CDM state — set externally by the CDM package (future phase)
	mu                  sync.RWMutex
	cdmEnabled          bool
	cdmSessionActive    bool
	cdmSessionExpiresAt *time.Time
}

// Config holds the initial poller configuration.
type Config struct {
	ServerURL    string
	AgentID      string
	PollInterval int // seconds
	Client       *http.Client
	Log          *slog.Logger
	JobHandler   JobHandler
}

// New creates a new Poller with the given configuration.
func New(cfg Config) *Poller {
	p := &Poller{
		serverURL:  cfg.ServerURL,
		agentID:    cfg.AgentID,
		client:     cfg.Client,
		log:        cfg.Log,
		startTime:  time.Now(),
		jobHandler: cfg.JobHandler,
	}
	p.pollInterval.Store(int64(cfg.PollInterval))
	return p
}

// Run starts the check-in loop. It blocks until the context is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	p.log.Info("poller started",
		slog.String("agent_id", p.agentID),
		slog.Int64("poll_interval", p.pollInterval.Load()),
	)

	// Do an initial check-in immediately
	p.checkin(ctx)

	for {
		interval := time.Duration(p.pollInterval.Load()) * time.Second
		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			timer.Stop()
			p.log.Info("poller stopped")
			return ctx.Err()
		case <-timer.C:
			p.checkin(ctx)
		}
	}
}

// SetCDMState updates the CDM state reported in check-ins.
func (p *Poller) SetCDMState(enabled, sessionActive bool, sessionExpiresAt *time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cdmEnabled = enabled
	p.cdmSessionActive = sessionActive
	p.cdmSessionExpiresAt = sessionExpiresAt
}

// PollInterval returns the current poll interval in seconds.
func (p *Poller) PollInterval() int {
	return int(p.pollInterval.Load())
}

// Sequence returns the current sequence number.
func (p *Poller) Sequence() int64 {
	return p.sequence.Load()
}

func (p *Poller) checkin(ctx context.Context) {
	seq := p.sequence.Add(1)

	p.mu.RLock()
	status := protocol.AgentStatus{
		UptimeSeconds:    int64(time.Since(p.startTime).Seconds()),
		CDMEnabled:       p.cdmEnabled,
		CDMSessionActive: p.cdmSessionActive,
		AgentVersion:     version.Version,
	}
	if p.cdmSessionExpiresAt != nil {
		t := *p.cdmSessionExpiresAt
		status.CDMSessionExpiresAt = &t
	}
	p.mu.RUnlock()

	req := protocol.CheckinRequest{
		AgentID:   p.agentID,
		Timestamp: time.Now().UTC(),
		Sequence:  seq,
		Status:    status,
		// InventoryDelta populated by inventory package in future phase
	}

	resp, err := p.postCheckin(ctx, &req)
	if err != nil {
		p.log.Error("check-in failed", slog.String("error", err.Error()), slog.Int64("seq", seq))
		return
	}

	p.log.Debug("check-in successful",
		slog.Int64("seq", seq),
		slog.Int("jobs", len(resp.Jobs)),
	)

	// Apply config updates from server
	if resp.Config != nil && resp.Config.PollIntervalSeconds > 0 {
		old := p.pollInterval.Swap(int64(resp.Config.PollIntervalSeconds))
		if old != int64(resp.Config.PollIntervalSeconds) {
			p.log.Info("poll interval updated",
				slog.Int("new", resp.Config.PollIntervalSeconds),
				slog.Int64("old", old),
			)
		}
	}

	// Dispatch jobs
	if p.jobHandler != nil {
		for _, job := range resp.Jobs {
			p.jobHandler(job)
		}
	}
}

func (p *Poller) postCheckin(ctx context.Context, checkin *protocol.CheckinRequest) (*protocol.CheckinResponse, error) {
	body, err := json.Marshal(checkin)
	if err != nil {
		return nil, fmt.Errorf("marshal check-in: %w", err)
	}

	url := strings.TrimRight(p.serverURL, "/") + "/v1/agents/checkin"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("POST checkin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed (HTTP 401): %s", string(respBody))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check-in failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var checkinResp protocol.CheckinResponse
	if err := json.Unmarshal(respBody, &checkinResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &checkinResp, nil
}

// ReadAgentID reads the persisted agent ID from disk.
func ReadAgentID(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-controlled path
	if err != nil {
		return "", fmt.Errorf("read agent_id: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("agent_id file is empty")
	}
	return id, nil
}
