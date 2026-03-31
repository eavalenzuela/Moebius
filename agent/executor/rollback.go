package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/eavalenzuela/Moebius/agent/update"
	"github.com/eavalenzuela/Moebius/shared/protocol"
)

// AgentRollbackPayload is the payload for agent_rollback jobs.
type AgentRollbackPayload struct {
	Reason             string `json:"reason,omitempty"`
	MinRollbackVersion string `json:"min_rollback_version,omitempty"`
}

func (e *Executor) executeAgentRollback(_ context.Context, payload json.RawMessage) protocol.JobResultSubmission {
	var p AgentRollbackPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid agent_rollback payload: " + err.Error(),
		}
	}

	if e.platform == nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "platform not configured",
		}
	}

	binaryPath := e.platform.BinaryPath()
	previousPath := e.platform.BinaryPreviousPath()
	pendingPath := e.platform.PendingUpdatePath()

	// Verify previous binary exists
	if _, err := os.Stat(previousPath); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: fmt.Sprintf("no previous binary at %s: cannot roll back", previousPath),
		}
	}

	// Perform rollback
	if err := update.Rollback(binaryPath, previousPath, pendingPath, e.log); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "rollback failed: " + err.Error(),
		}
	}

	// Return restarting — the runJob handler will trigger service restart
	return protocol.JobResultSubmission{
		Status:  "restarting",
		Message: fmt.Sprintf("Rolled back to previous binary. Reason: %s", p.Reason),
	}
}
