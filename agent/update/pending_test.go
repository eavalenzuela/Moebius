package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending_update.json")

	p := &PendingUpdate{
		JobID:           "job_abc123",
		ExpectedVersion: "1.5.0",
		PreviousVersion: "1.4.2",
		Deadline:        time.Now().Add(90 * time.Second).Truncate(time.Millisecond),
	}

	if err := WritePending(path, p); err != nil {
		t.Fatal(err)
	}

	got, err := ReadPending(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil pending update")
	}
	if got.JobID != p.JobID {
		t.Errorf("job_id: got %s, want %s", got.JobID, p.JobID)
	}
	if got.ExpectedVersion != p.ExpectedVersion {
		t.Errorf("expected_version: got %s, want %s", got.ExpectedVersion, p.ExpectedVersion)
	}
	if got.PreviousVersion != p.PreviousVersion {
		t.Errorf("previous_version: got %s, want %s", got.PreviousVersion, p.PreviousVersion)
	}
}

func TestReadPending_NotExist(t *testing.T) {
	got, err := ReadPending("/nonexistent/path/pending.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestRemovePending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending_update.json")

	// Write a file
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemovePending(path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}

	// Removing again should not error
	if err := RemovePending(path); err != nil {
		t.Errorf("expected no error on missing file, got %v", err)
	}
}
