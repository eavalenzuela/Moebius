// Package localcli implements the agent's local CLI commands and the
// daemon-side IPC methods they call.
package localcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/eavalenzuela/Moebius/agent/cdm"
	agentconfig "github.com/eavalenzuela/Moebius/agent/config"
	"github.com/eavalenzuela/Moebius/agent/ipc"
	"github.com/eavalenzuela/Moebius/agent/localaudit"
	"github.com/eavalenzuela/Moebius/shared/version"
)

// StatusResult is returned by "agent.status".
type StatusResult struct {
	AgentID       string `json:"agent_id"`
	Version       string `json:"version"`
	ServerURL     string `json:"server_url"`
	PollInterval  int    `json:"poll_interval"`
	CDMEnabled    bool   `json:"cdm_enabled"`
	SessionActive bool   `json:"session_active"`
}

// CDMStatusResult is returned by "cdm.status".
type CDMStatusResult struct {
	Enabled          bool   `json:"enabled"`
	SessionActive    bool   `json:"session_active"`
	SessionExpiresAt string `json:"session_expires_at,omitempty"`
	SessionGrantedBy string `json:"session_granted_by,omitempty"`
	SessionGrantedAt string `json:"session_granted_at,omitempty"`
}

// LogsParams is the request body for "agent.logs".
type LogsParams struct {
	Tail int `json:"tail"`
}

// LogsResult is returned by "agent.logs".
type LogsResult struct {
	Lines []string `json:"lines"`
}

// AuditResult is returned by "agent.audit".
type AuditResult struct {
	Entries []localaudit.Entry `json:"entries"`
}

// DaemonState holds references needed by daemon-side IPC handlers.
type DaemonState struct {
	AgentID    string
	Config     *agentconfig.Config
	CDMManager *cdm.Manager
	AuditLog   *localaudit.Logger
	LogFile    string // path to agent log file
}

// RegisterIPC registers daemon-side IPC methods for CLI operations.
// These require a valid session token, verified by the auth middleware
// wrapper passed in.
func RegisterIPC(router *ipc.Router, state *DaemonState, requireAuth func(ipc.HandlerFunc) ipc.HandlerFunc) {
	router.Handle("agent.status", requireAuth(func(ctx context.Context, _ json.RawMessage) (any, error) {
		snap := state.CDMManager.Snapshot()
		if state.AuditLog != nil {
			// Determine interface from context — CLI calls include a token.
			state.AuditLog.LogConfigView("", localaudit.InterfaceCLI)
		}
		return StatusResult{
			AgentID:       state.AgentID,
			Version:       version.FullVersion(),
			ServerURL:     state.Config.Server.URL,
			PollInterval:  state.Config.Server.PollIntervalSeconds,
			CDMEnabled:    snap.Enabled,
			SessionActive: snap.SessionActive,
		}, nil
	}))

	router.Handle("cdm.status", requireAuth(func(_ context.Context, _ json.RawMessage) (any, error) {
		snap := state.CDMManager.Snapshot()
		result := CDMStatusResult{
			Enabled:       snap.Enabled,
			SessionActive: snap.SessionActive,
		}
		if snap.SessionExpiresAt != nil {
			result.SessionExpiresAt = snap.SessionExpiresAt.Format(time.RFC3339)
		}
		if snap.SessionGrantedBy != "" {
			result.SessionGrantedBy = snap.SessionGrantedBy
		}
		if snap.SessionGrantedAt != nil {
			result.SessionGrantedAt = snap.SessionGrantedAt.Format(time.RFC3339)
		}
		return result, nil
	}))

	router.Handle("cdm.enable", requireAuth(func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Actor string `json:"actor"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}
		if err := state.CDMManager.Enable(p.Actor); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
		}
		if state.AuditLog != nil {
			state.AuditLog.LogCDMToggle(p.Actor, localaudit.InterfaceCLI, "disabled", "enabled")
		}
		return map[string]bool{"ok": true}, nil
	}))

	router.Handle("cdm.disable", requireAuth(func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Actor string `json:"actor"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}
		if err := state.CDMManager.Disable(p.Actor); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
		}
		if state.AuditLog != nil {
			state.AuditLog.LogCDMToggle(p.Actor, localaudit.InterfaceCLI, "enabled", "disabled")
		}
		return map[string]bool{"ok": true}, nil
	}))

	router.Handle("cdm.grant", requireAuth(func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Actor    string `json:"actor"`
			Duration string `json:"duration"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}
		dur, err := time.ParseDuration(p.Duration)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "invalid duration: " + err.Error()}
		}
		if dur <= 0 {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "duration must be positive"}
		}
		if err := state.CDMManager.GrantSession(p.Actor, dur); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
		}
		if state.AuditLog != nil {
			expires := time.Now().UTC().Add(dur)
			state.AuditLog.LogCDMGrant(p.Actor, localaudit.InterfaceCLI, p.Duration, &expires)
		}
		return map[string]bool{"ok": true}, nil
	}))

	router.Handle("cdm.revoke", requireAuth(func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Actor string `json:"actor"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: err.Error()}
		}
		if err := state.CDMManager.RevokeSession(p.Actor); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
		}
		if state.AuditLog != nil {
			state.AuditLog.LogCDMRevoke(p.Actor, localaudit.InterfaceCLI, "user request")
		}
		return map[string]bool{"ok": true}, nil
	}))

	router.Handle("agent.logs", requireAuth(func(_ context.Context, params json.RawMessage) (any, error) {
		var p LogsParams
		if params != nil {
			_ = json.Unmarshal(params, &p)
		}
		if p.Tail <= 0 {
			p.Tail = 50
		}

		lines, err := readTailLines(state.LogFile, p.Tail)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
		}
		return LogsResult{Lines: lines}, nil
	}))

	router.Handle("agent.audit", requireAuth(func(_ context.Context, _ json.RawMessage) (any, error) {
		// CLI shows CDM-filtered view (accessible to any authenticated local user).
		if state.AuditLog == nil {
			return AuditResult{Entries: []localaudit.Entry{}}, nil
		}
		entries, err := state.AuditLog.ReadCDMOnly(100)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: err.Error()}
		}
		if entries == nil {
			entries = []localaudit.Entry{}
		}
		return AuditResult{Entries: entries}, nil
	}))
}

// readTailLines reads the last n lines from a file.
func readTailLines(path string, n int) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("no log file configured")
	}
	f, err := os.Open(path) //nolint:gosec // agent-controlled path
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var all []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read log file: %w", err)
	}

	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
