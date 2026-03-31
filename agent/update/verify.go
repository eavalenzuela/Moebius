package update

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/eavalenzuela/Moebius/shared/version"
)

// VerifyResult describes the outcome of a post-restart verification check.
type VerifyResult struct {
	Pending  *PendingUpdate // the pending update that was found (nil if none)
	Success  bool           // true if version matches expected
	NeedRoll bool           // true if rollback is required
	Error    string         // human-readable error description
}

// CheckPostRestart reads the pending_update.json and verifies the running version.
// Returns nil result if no pending update exists.
func CheckPostRestart(pendingPath string, log *slog.Logger) *VerifyResult {
	pending, err := ReadPending(pendingPath)
	if err != nil {
		log.Error("failed to read pending update", slog.String("error", err.Error()))
		return nil
	}
	if pending == nil {
		return nil // no pending update
	}

	log.Info("pending update found",
		slog.String("expected", pending.ExpectedVersion),
		slog.String("job_id", pending.JobID),
	)

	currentVersion := version.Version

	// Check if deadline has passed
	if time.Now().After(pending.Deadline) {
		msg := fmt.Sprintf("update deadline exceeded: expected %s by %s, running %s",
			pending.ExpectedVersion, pending.Deadline.Format(time.RFC3339), currentVersion)
		log.Error(msg)
		return &VerifyResult{
			Pending:  pending,
			NeedRoll: true,
			Error:    msg,
		}
	}

	// Check version match
	if currentVersion != pending.ExpectedVersion {
		msg := fmt.Sprintf("version mismatch after restart: expected %s, got %s",
			pending.ExpectedVersion, currentVersion)
		log.Error(msg)
		return &VerifyResult{
			Pending:  pending,
			NeedRoll: true,
			Error:    msg,
		}
	}

	log.Info("post-restart verification passed", slog.String("version", currentVersion))
	return &VerifyResult{
		Pending: pending,
		Success: true,
	}
}

// Rollback restores the previous binary and removes the pending update file.
// Returns an error description suitable for reporting to the server.
func Rollback(binaryPath, previousPath, pendingPath string, log *slog.Logger) error {
	// Verify previous binary exists
	if _, err := os.Stat(previousPath); err != nil {
		return fmt.Errorf("previous binary not found at %s: %w", previousPath, err)
	}

	// Atomic swap: rename previous → current
	if err := os.Rename(previousPath, binaryPath); err != nil {
		return fmt.Errorf("rollback rename failed: %w", err)
	}

	log.Info("binary rolled back", slog.String("restored", binaryPath))

	// Clean up pending update file
	_ = RemovePending(pendingPath)

	return nil
}
