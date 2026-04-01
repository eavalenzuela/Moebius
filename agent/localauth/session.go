package localauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// SessionType determines the expiry policy for a session.
type SessionType int

const (
	// SessionCLI is a CLI session: 15 min idle-refreshed.
	SessionCLI SessionType = iota
	// SessionWeb is a web session: 30 min idle / 8 hr absolute.
	SessionWeb
)

const (
	cliIdleTimeout = 15 * time.Minute
	webIdleTimeout = 30 * time.Minute
	webMaxLifetime = 8 * time.Hour
	tokenBytes     = 32 // 256-bit token
)

// Session represents an authenticated user session.
type Session struct {
	Token     string
	Username  string
	Type      SessionType
	CreatedAt time.Time
	LastUsed  time.Time
	ExpiresAt time.Time // absolute expiry (Web only; CLI uses idle-only)
}

// SessionManager stores and validates in-memory sessions.
// All sessions are lost on agent restart (by design per spec).
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewSessionManager creates an empty SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]*Session)}
}

// Create issues a new session token for the given user and type.
func (sm *SessionManager) Create(username string, st SessionType) (*Session, error) {
	token, err := generateToken()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	s := &Session{
		Token:    token,
		Username: username,
		Type:     st,
		CreatedAt: now,
		LastUsed:  now,
	}

	switch st {
	case SessionCLI:
		s.ExpiresAt = now.Add(cliIdleTimeout)
	case SessionWeb:
		s.ExpiresAt = now.Add(webMaxLifetime)
	}

	sm.mu.Lock()
	sm.sessions[token] = s
	sm.mu.Unlock()

	return s, nil
}

// Validate checks if token is valid and refreshes the idle timeout.
// Returns the session on success, or an error if expired/unknown.
func (sm *SessionManager) Validate(token string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok := sm.sessions[token]
	if !ok {
		return nil, fmt.Errorf("unknown session token")
	}

	now := time.Now().UTC()

	// Check absolute expiry (applies to both types, but effectively
	// only matters for Web since CLI ExpiresAt is reset on each use).
	if now.After(s.ExpiresAt) {
		delete(sm.sessions, token)
		return nil, fmt.Errorf("session expired")
	}

	// Check idle timeout.
	var idleTimeout time.Duration
	switch s.Type {
	case SessionCLI:
		idleTimeout = cliIdleTimeout
	case SessionWeb:
		idleTimeout = webIdleTimeout
	}

	if now.Sub(s.LastUsed) > idleTimeout {
		delete(sm.sessions, token)
		return nil, fmt.Errorf("session idle timeout")
	}

	// Refresh idle timer.
	s.LastUsed = now

	// For CLI sessions, also push ExpiresAt forward (idle-refreshed).
	if s.Type == SessionCLI {
		s.ExpiresAt = now.Add(cliIdleTimeout)
	}

	return s, nil
}

// Revoke invalidates a session by token.
func (sm *SessionManager) Revoke(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// RevokeAll invalidates all sessions (e.g. on agent restart).
func (sm *SessionManager) RevokeAll() {
	sm.mu.Lock()
	sm.sessions = make(map[string]*Session)
	sm.mu.Unlock()
}

// Cleanup removes all expired sessions. Call periodically.
func (sm *SessionManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now().UTC()
	for token, s := range sm.sessions {
		expired := now.After(s.ExpiresAt)

		var idleTimeout time.Duration
		switch s.Type {
		case SessionCLI:
			idleTimeout = cliIdleTimeout
		case SessionWeb:
			idleTimeout = webIdleTimeout
		}
		idle := now.Sub(s.LastUsed) > idleTimeout

		if expired || idle {
			delete(sm.sessions, token)
		}
	}
}

func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
