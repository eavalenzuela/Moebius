package localaudit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	l := New(filepath.Join(dir, "audit.log"))

	l.LogAuthSuccess("alice", InterfaceCLI)
	l.LogAuthFailure("bob", InterfaceUI, "bad password")
	l.LogCDMToggle("alice", InterfaceCLI, "disabled", "enabled")

	now := time.Now().UTC()
	l.LogCDMGrant("alice", InterfaceCLI, "10m", &now)
	l.LogCDMRevoke("alice", InterfaceUI, "user request")
	l.LogConfigView("alice", InterfaceCLI)

	entries, err := l.Read(100)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("len = %d, want 6", len(entries))
	}

	// Most recent first.
	if entries[0].Action != ActionConfigView {
		t.Errorf("entries[0].Action = %q, want %q", entries[0].Action, ActionConfigView)
	}
	if entries[5].Action != ActionAuthSuccess {
		t.Errorf("entries[5].Action = %q, want %q", entries[5].Action, ActionAuthSuccess)
	}
}

func TestReadWithLimit(t *testing.T) {
	dir := t.TempDir()
	l := New(filepath.Join(dir, "audit.log"))

	for i := range 10 {
		if i%2 == 0 {
			l.LogAuthSuccess("user", InterfaceCLI)
		} else {
			l.LogCDMToggle("user", InterfaceCLI, "off", "on")
		}
	}

	entries, err := l.Read(3)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len = %d, want 3", len(entries))
	}
}

func TestReadCDMOnly(t *testing.T) {
	dir := t.TempDir()
	l := New(filepath.Join(dir, "audit.log"))

	l.LogAuthSuccess("alice", InterfaceCLI)
	l.LogAuthFailure("bob", InterfaceUI, "bad pass")
	l.LogCDMToggle("alice", InterfaceCLI, "disabled", "enabled")

	now := time.Now().UTC()
	l.LogCDMGrant("alice", InterfaceCLI, "10m", &now)
	l.LogConfigView("alice", InterfaceCLI)
	l.LogCDMRevoke("alice", InterfaceUI, "user request")

	entries, err := l.ReadCDMOnly(100)
	if err != nil {
		t.Fatalf("ReadCDMOnly: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("CDM entries = %d, want 3", len(entries))
	}

	// Most recent first.
	if entries[0].Action != ActionCDMRevoke {
		t.Errorf("entries[0] = %q, want %q", entries[0].Action, ActionCDMRevoke)
	}
	if entries[1].Action != ActionCDMGrant {
		t.Errorf("entries[1] = %q, want %q", entries[1].Action, ActionCDMGrant)
	}
	if entries[2].Action != ActionCDMToggle {
		t.Errorf("entries[2] = %q, want %q", entries[2].Action, ActionCDMToggle)
	}
}

func TestReadNonexistentFile(t *testing.T) {
	l := New("/tmp/nonexistent-audit-file-test.log")
	entries, err := l.Read(10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %v", entries)
	}
}

func TestEntryFields(t *testing.T) {
	dir := t.TempDir()
	l := New(filepath.Join(dir, "audit.log"))

	l.LogAuthSuccess("alice", InterfaceUI)

	entries, _ := l.Read(1)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}

	e := entries[0]
	if e.Action != ActionAuthSuccess {
		t.Errorf("Action = %q", e.Action)
	}
	if e.Username != "alice" {
		t.Errorf("Username = %q", e.Username)
	}
	if e.Interface != InterfaceUI {
		t.Errorf("Interface = %q", e.Interface)
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	l := New(path)

	l.LogAuthSuccess("test", InterfaceCLI)

	// File should be created with 0600 permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}
