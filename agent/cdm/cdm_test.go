package cdm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "cdm.json")
	auditPath := filepath.Join(dir, "cdm-audit.log")
	audit := NewAuditLog(auditPath)
	m, err := New(statePath, audit)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestDisabledByDefault(t *testing.T) {
	m := tempManager(t)
	if m.Enabled() {
		t.Error("expected CDM disabled by default")
	}
	if !m.CanExecuteJob() {
		t.Error("expected jobs allowed when CDM disabled")
	}
}

func TestEnableDisable(t *testing.T) {
	m := tempManager(t)

	if err := m.Enable("admin"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !m.Enabled() {
		t.Error("expected CDM enabled")
	}
	if m.CanExecuteJob() {
		t.Error("expected jobs blocked when CDM enabled without session")
	}

	if err := m.Disable("admin"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if m.Enabled() {
		t.Error("expected CDM disabled")
	}
	if !m.CanExecuteJob() {
		t.Error("expected jobs allowed when CDM disabled")
	}
}

func TestGrantSession(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")

	if err := m.GrantSession("tech", 10*time.Minute); err != nil {
		t.Fatalf("GrantSession: %v", err)
	}
	if !m.SessionActive() {
		t.Error("expected session active")
	}
	if !m.CanExecuteJob() {
		t.Error("expected jobs allowed during session")
	}
	if m.SessionExpiresAt() == nil {
		t.Error("expected non-nil expiry")
	}
}

func TestGrantSession_RequiresEnabled(t *testing.T) {
	m := tempManager(t)
	if err := m.GrantSession("tech", 10*time.Minute); err == nil {
		t.Error("expected error when granting session with CDM disabled")
	}
}

func TestRevokeSession(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	_ = m.GrantSession("tech", 10*time.Minute)

	if err := m.RevokeSession("tech"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if m.SessionActive() {
		t.Error("expected session revoked")
	}
	if m.CanExecuteJob() {
		t.Error("expected jobs blocked after session revoked")
	}
}

func TestRevokeSession_NoSession(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	if err := m.RevokeSession("tech"); err == nil {
		t.Error("expected error when revoking without active session")
	}
}

func TestSessionExpiry(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	_ = m.GrantSession("tech", 1*time.Millisecond)

	time.Sleep(5 * time.Millisecond)

	if m.SessionActive() {
		t.Error("expected session expired")
	}
	if m.CanExecuteJob() {
		t.Error("expected jobs blocked after session expired")
	}
}

func TestDisableClearsSession(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	_ = m.GrantSession("tech", 10*time.Minute)

	_ = m.Disable("admin")
	if m.SessionActive() {
		t.Error("expected session cleared on disable")
	}
	if !m.CanExecuteJob() {
		t.Error("expected jobs allowed when CDM disabled")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "cdm.json")
	auditPath := filepath.Join(dir, "cdm-audit.log")
	audit := NewAuditLog(auditPath)

	m1, _ := New(statePath, audit)
	_ = m1.Enable("admin")
	_ = m1.GrantSession("tech", 10*time.Minute)

	// Load from same file
	m2, err := New(statePath, audit)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !m2.Enabled() {
		t.Error("expected enabled after reload")
	}
	if !m2.SessionActive() {
		t.Error("expected session active after reload")
	}
}

func TestSnapshot(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	_ = m.GrantSession("tech", 10*time.Minute)

	s := m.Snapshot()
	if !s.Enabled || !s.SessionActive || s.SessionGrantedBy != "tech" {
		t.Errorf("unexpected snapshot: %+v", s)
	}
}

func TestAuditLog(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "cdm-audit.log")
	audit := NewAuditLog(auditPath)

	audit.Write(AuditEntry{Action: "cdm.enable", Actor: "admin"})
	audit.Write(AuditEntry{Action: "cdm.session.grant", Actor: "tech"})

	entries, err := audit.Read(10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].Action != "cdm.session.grant" {
		t.Errorf("expected most recent first, got %s", entries[0].Action)
	}
}

func TestAuditLog_ReadNonExistent(t *testing.T) {
	audit := NewAuditLog(filepath.Join(os.TempDir(), "nonexistent-cdm-audit.log"))
	entries, err := audit.Read(10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 0 {
		t.Error("expected empty for non-existent file")
	}
}

// --- P1: splitLines tests ---

func TestSplitLines_Empty(t *testing.T) {
	lines := splitLines(nil)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
	lines = splitLines([]byte{})
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for empty slice, got %d", len(lines))
	}
}

func TestSplitLines_SingleLine_NoNewline(t *testing.T) {
	lines := splitLines([]byte(`{"action":"test"}`))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if string(lines[0]) != `{"action":"test"}` {
		t.Errorf("got %q", string(lines[0]))
	}
}

func TestSplitLines_TrailingNewline(t *testing.T) {
	lines := splitLines([]byte("line1\nline2\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (trailing newline should not produce empty line), got %d", len(lines))
	}
}

func TestSplitLines_MultipleLines(t *testing.T) {
	lines := splitLines([]byte("a\nb\nc"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if string(lines[0]) != "a" || string(lines[1]) != "b" || string(lines[2]) != "c" {
		t.Errorf("got %q %q %q", string(lines[0]), string(lines[1]), string(lines[2]))
	}
}

// --- P1: stateLabel tests ---

func TestStateLabel(t *testing.T) {
	cases := []struct {
		enabled, session bool
		want             string
	}{
		{false, false, "disabled"},
		{false, true, "disabled"},
		{true, false, "enabled"},
		{true, true, "enabled+session"},
	}
	for _, tc := range cases {
		got := stateLabel(tc.enabled, tc.session)
		if got != tc.want {
			t.Errorf("stateLabel(%v, %v) = %q, want %q", tc.enabled, tc.session, got, tc.want)
		}
	}
}

// --- P2: CDM edge cases ---

func TestDoubleEnable(t *testing.T) {
	m := tempManager(t)
	if err := m.Enable("admin"); err != nil {
		t.Fatalf("first Enable: %v", err)
	}
	if err := m.Enable("admin"); err != nil {
		t.Fatalf("second Enable: %v", err)
	}
	if !m.Enabled() {
		t.Error("expected still enabled")
	}
}

func TestDoubleDisable(t *testing.T) {
	m := tempManager(t)
	// Disable when already disabled
	if err := m.Disable("admin"); err != nil {
		t.Fatalf("Disable when already disabled: %v", err)
	}
	if m.Enabled() {
		t.Error("expected still disabled")
	}
}

func TestSessionReplacement(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	_ = m.GrantSession("tech1", 10*time.Minute)

	// Grant new session while one is active
	if err := m.GrantSession("tech2", 5*time.Minute); err != nil {
		t.Fatalf("replacement GrantSession: %v", err)
	}
	snap := m.Snapshot()
	if snap.SessionGrantedBy != "tech2" {
		t.Errorf("expected tech2, got %s", snap.SessionGrantedBy)
	}
}

func TestAuditLog_ReadWithLimit(t *testing.T) {
	dir := t.TempDir()
	audit := NewAuditLog(filepath.Join(dir, "audit.log"))

	for i := 0; i < 5; i++ {
		audit.Write(AuditEntry{Action: fmt.Sprintf("action_%d", i)})
	}

	entries, err := audit.Read(2)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].Action != "action_4" {
		t.Errorf("expected action_4, got %s", entries[0].Action)
	}
	if entries[1].Action != "action_3" {
		t.Errorf("expected action_3, got %s", entries[1].Action)
	}
}

func TestAuditLog_ReadCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	// Write valid entry, then corrupt line, then valid entry
	data := `{"action":"good1"}` + "\nnot json\n" + `{"action":"good2"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	audit := NewAuditLog(path)
	entries, err := audit.Read(10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (skip corrupt), got %d", len(entries))
	}
}

func TestCanExecuteJob_DisabledAlwaysAllows(t *testing.T) {
	m := tempManager(t)
	if !m.CanExecuteJob() {
		t.Error("disabled CDM should always allow jobs")
	}
}

func TestCanExecuteJob_EnabledNoSession(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	if m.CanExecuteJob() {
		t.Error("enabled CDM without session should block jobs")
	}
}

func TestCanExecuteJob_EnabledWithSession(t *testing.T) {
	m := tempManager(t)
	_ = m.Enable("admin")
	_ = m.GrantSession("tech", 10*time.Minute)
	if !m.CanExecuteJob() {
		t.Error("enabled CDM with active session should allow jobs")
	}
}
