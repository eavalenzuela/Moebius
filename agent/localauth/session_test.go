package localauth

import (
	"testing"
	"time"
)

func TestSessionCreate(t *testing.T) {
	sm := NewSessionManager()

	sess, err := sm.Create("alice", SessionCLI)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.Username != "alice" {
		t.Errorf("Username = %q, want %q", sess.Username, "alice")
	}
	if sess.Token == "" {
		t.Error("Token is empty")
	}
	if sess.Type != SessionCLI {
		t.Errorf("Type = %d, want %d", sess.Type, SessionCLI)
	}
}

func TestSessionValidateAndRefresh(t *testing.T) {
	sm := NewSessionManager()

	sess, _ := sm.Create("bob", SessionCLI)

	// Valid immediately.
	got, err := sm.Validate(sess.Token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.Username != "bob" {
		t.Errorf("Username = %q, want %q", got.Username, "bob")
	}
}

func TestSessionUnknownToken(t *testing.T) {
	sm := NewSessionManager()
	_, err := sm.Validate("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
}

func TestSessionRevoke(t *testing.T) {
	sm := NewSessionManager()

	sess, _ := sm.Create("charlie", SessionWeb)

	sm.Revoke(sess.Token)

	_, err := sm.Validate(sess.Token)
	if err == nil {
		t.Fatal("expected error after revocation")
	}
}

func TestSessionRevokeAll(t *testing.T) {
	sm := NewSessionManager()
	s1, _ := sm.Create("a", SessionCLI)
	s2, _ := sm.Create("b", SessionWeb)

	sm.RevokeAll()

	if _, err := sm.Validate(s1.Token); err == nil {
		t.Error("session 1 should be revoked")
	}
	if _, err := sm.Validate(s2.Token); err == nil {
		t.Error("session 2 should be revoked")
	}
}

func TestSessionTokenUniqueness(t *testing.T) {
	sm := NewSessionManager()
	seen := make(map[string]bool)
	for range 100 {
		s, err := sm.Create("user", SessionCLI)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if seen[s.Token] {
			t.Fatalf("duplicate token: %s", s.Token)
		}
		seen[s.Token] = true
	}
}

func TestSessionCleanup(t *testing.T) {
	sm := NewSessionManager()

	// Create a session and manually backdate it.
	sess, _ := sm.Create("cleanup-test", SessionCLI)

	sm.mu.Lock()
	s := sm.sessions[sess.Token]
	s.LastUsed = time.Now().UTC().Add(-20 * time.Minute)
	s.ExpiresAt = time.Now().UTC().Add(-5 * time.Minute)
	sm.mu.Unlock()

	sm.Cleanup()

	if _, err := sm.Validate(sess.Token); err == nil {
		t.Fatal("expected session to be cleaned up")
	}
}

func TestSessionWebTypes(t *testing.T) {
	sm := NewSessionManager()

	sess, _ := sm.Create("webuser", SessionWeb)
	if sess.Type != SessionWeb {
		t.Errorf("Type = %d, want %d", sess.Type, SessionWeb)
	}

	// Verify ExpiresAt is approximately 8 hours from now.
	expected := time.Now().UTC().Add(8 * time.Hour)
	diff := sess.ExpiresAt.Sub(expected)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("ExpiresAt = %v, want ~%v (diff %v)", sess.ExpiresAt, expected, diff)
	}
}
