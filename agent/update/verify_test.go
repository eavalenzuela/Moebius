package update

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/shared/version"
)

func TestCheckPostRestart_NoPending(t *testing.T) {
	result := CheckPostRestart("/nonexistent/path", slog.Default())
	if result != nil {
		t.Errorf("expected nil result for missing pending file")
	}
}

func TestCheckPostRestart_VersionMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending_update.json")

	// Set expected version to match current
	p := &PendingUpdate{
		JobID:           "job_test1",
		ExpectedVersion: version.Version, // should match "dev" in test builds
		PreviousVersion: "1.0.0",
		Deadline:        time.Now().Add(5 * time.Minute),
	}
	if err := WritePending(path, p); err != nil {
		t.Fatal(err)
	}

	result := CheckPostRestart(path, slog.Default())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.NeedRoll {
		t.Error("should not need rollback")
	}
}

func TestCheckPostRestart_VersionMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending_update.json")

	p := &PendingUpdate{
		JobID:           "job_test2",
		ExpectedVersion: "99.99.99", // won't match "dev"
		PreviousVersion: "1.0.0",
		Deadline:        time.Now().Add(5 * time.Minute),
	}
	if err := WritePending(path, p); err != nil {
		t.Fatal(err)
	}

	result := CheckPostRestart(path, slog.Default())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected failure")
	}
	if !result.NeedRoll {
		t.Error("should need rollback")
	}
}

func TestCheckPostRestart_DeadlineExceeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending_update.json")

	p := &PendingUpdate{
		JobID:           "job_test3",
		ExpectedVersion: version.Version, // version matches but deadline is past
		PreviousVersion: "1.0.0",
		Deadline:        time.Now().Add(-1 * time.Minute), // expired
	}
	if err := WritePending(path, p); err != nil {
		t.Fatal(err)
	}

	result := CheckPostRestart(path, slog.Default())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Success {
		t.Error("expected failure due to deadline")
	}
	if !result.NeedRoll {
		t.Error("should need rollback")
	}
}

func TestRollback_Success(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "agent")
	previousPath := filepath.Join(dir, "agent.previous")
	pendingPath := filepath.Join(dir, "pending_update.json")

	// Create "binaries"
	if err := os.WriteFile(binaryPath, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previousPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Rollback(binaryPath, previousPath, pendingPath, slog.Default()); err != nil {
		t.Fatal(err)
	}

	// Verify binary was restored
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-binary" {
		t.Errorf("expected old-binary, got %s", string(data))
	}

	// Verify previous was consumed (renamed, so no longer exists)
	if _, err := os.Stat(previousPath); !os.IsNotExist(err) {
		t.Error("previous binary should be gone after rename")
	}

	// Verify pending file removed
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Error("pending file should be removed")
	}
}

func TestRollback_NoPrevious(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "agent")
	previousPath := filepath.Join(dir, "agent.previous") // doesn't exist
	pendingPath := filepath.Join(dir, "pending_update.json")

	if err := os.WriteFile(binaryPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Rollback(binaryPath, previousPath, pendingPath, slog.Default())
	if err == nil {
		t.Fatal("expected error when previous binary missing")
	}
}
