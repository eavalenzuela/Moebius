package protocol

import "time"

// LogShipment is sent by the agent to ship logs to the server
// (POST /v1/agents/logs).
type LogShipment struct {
	AgentID string     `json:"agent_id"`
	Entries []LogEntry `json:"entries"`
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // "debug", "info", "warn", "error"
	Message   string    `json:"message"`
}
