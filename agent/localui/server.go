package localui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/eavalenzuela/Moebius/agent/cdm"
	"github.com/eavalenzuela/Moebius/agent/localaudit"
	"github.com/eavalenzuela/Moebius/agent/localauth"
	"github.com/eavalenzuela/Moebius/shared/version"
)

//go:embed static/*
var staticFS embed.FS

const (
	sessionCookieName = "moebius_session"
)

// ServerConfig holds configuration for the local web UI server.
type ServerConfig struct {
	Port    int
	DataDir string // for local CA storage
	LogDir  string // for reading agent logs
}

// Server is the localhost-only HTTPS web UI server.
type Server struct {
	cfg       ServerConfig
	auth      localauth.Authenticator
	sessions  *localauth.SessionManager
	cdmMgr    *cdm.Manager
	localCA   *LocalCA
	audit     *localaudit.Logger
	log       *slog.Logger
	agentID   string
	serverURL string
	addr      string        // actual listen address (populated after Serve starts)
	ready     chan struct{} // closed when server is listening
}

// NewServer creates a local web UI server.
func NewServer(
	cfg ServerConfig,
	auth localauth.Authenticator,
	sessions *localauth.SessionManager,
	cdmMgr *cdm.Manager,
	audit *localaudit.Logger,
	log *slog.Logger,
	agentID string,
	serverURL string,
) *Server {
	return &Server{
		cfg:       cfg,
		auth:      auth,
		sessions:  sessions,
		cdmMgr:    cdmMgr,
		audit:     audit,
		localCA:   NewLocalCA(cfg.DataDir),
		log:       log,
		agentID:   agentID,
		serverURL: serverURL,
		ready:     make(chan struct{}),
	}
}

// Serve starts the HTTPS server on 127.0.0.1:<port>. Blocks until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	// Ensure local CA and cert exist.
	if err := s.localCA.EnsureCA(); err != nil {
		return fmt.Errorf("ensure local CA: %w", err)
	}
	if err := s.localCA.EnsureCert(); err != nil {
		return fmt.Errorf("ensure localhost cert: %w", err)
	}

	// Best-effort trust store installation.
	if err := InstallCATrustStore(s.localCA.CACertPath()); err != nil {
		s.log.Warn("could not install CA into trust store (browsers may show warnings)",
			slog.String("error", err.Error()))
	}

	tlsCfg, err := s.localCA.TLSConfig()
	if err != nil {
		return fmt.Errorf("load TLS config: %w", err)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.addr = ln.Addr().String()
	close(s.ready)

	srv := &http.Server{
		Handler:      mux,
		TLSConfig:    tlsCfg,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.log.Info("local web UI listening", slog.String("addr", "https://"+s.addr))

	go func() { //nolint:gosec // intentional: need fresh context for graceful shutdown after parent cancel
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// ServeTLS with empty certFile/keyFile since we set TLSConfig.Certificates directly.
	if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// CACertPath exposes the local CA cert path for trust store installation at install time.
func (s *Server) CACertPath() string {
	return s.localCA.CACertPath()
}

// Addr returns the actual listen address (host:port). Only valid after Ready().
func (s *Server) Addr() string {
	return s.addr
}

// Ready returns a channel that is closed when the server is listening.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Static files (login page, status page, etc.)
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		s.log.Error("failed to create static sub-fs", slog.String("error", err.Error()))
		return
	}
	fileServer := http.FileServer(http.FS(staticSub))

	// API endpoints
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /api/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("GET /api/cdm", s.requireAuth(s.handleCDMStatus))
	mux.HandleFunc("POST /api/cdm/enable", s.requireAuth(s.handleCDMEnable))
	mux.HandleFunc("POST /api/cdm/disable", s.requireAuth(s.handleCDMDisable))
	mux.HandleFunc("POST /api/cdm/grant", s.requireAuth(s.handleCDMGrant))
	mux.HandleFunc("POST /api/cdm/revoke", s.requireAuth(s.handleCDMRevoke))
	mux.HandleFunc("GET /api/audit", s.requireAuth(s.handleAuditLog))

	// Serve static files for everything else.
	mux.Handle("/", fileServer)
}

// --- Auth middleware ---

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		sess, err := s.sessions.Validate(cookie.Value)
		if err != nil {
			// Clear the invalid cookie.
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteStrictMode,
			})
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
			return
		}

		// Store username in request context for handlers.
		ctx := context.WithValue(r.Context(), ctxKeyUsername, sess.Username)
		next(w, r.WithContext(ctx))
	}
}

type contextKey string

const ctxKeyUsername contextKey = "username"

func usernameFromCtx(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyUsername).(string); ok {
		return v
	}
	return "unknown"
}

// --- Handlers ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	if err := s.auth.Authenticate(req.Username, req.Password); err != nil {
		s.log.Info("local UI auth failure", slog.String("username", req.Username))
		if s.audit != nil {
			s.audit.LogAuthFailure(req.Username, localaudit.InterfaceUI, "invalid credentials")
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	sess, err := s.sessions.Create(req.Username, localauth.SessionWeb)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create session failed"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	s.log.Info("local UI auth success", slog.String("username", req.Username))
	if s.audit != nil {
		s.audit.LogAuthSuccess(req.Username, localaudit.InterfaceUI)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "username": req.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.Revoke(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := s.cdmMgr.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id":       s.agentID,
		"version":        version.FullVersion(),
		"server_url":     s.serverURL,
		"cdm_enabled":    snap.Enabled,
		"session_active": snap.SessionActive,
	})
}

func (s *Server) handleCDMStatus(w http.ResponseWriter, _ *http.Request) {
	snap := s.cdmMgr.Snapshot()
	result := map[string]any{
		"enabled":        snap.Enabled,
		"session_active": snap.SessionActive,
	}
	if snap.SessionExpiresAt != nil {
		result["session_expires_at"] = snap.SessionExpiresAt.Format(time.RFC3339)
	}
	if snap.SessionGrantedBy != "" {
		result["session_granted_by"] = snap.SessionGrantedBy
	}
	if snap.SessionGrantedAt != nil {
		result["session_granted_at"] = snap.SessionGrantedAt.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCDMEnable(w http.ResponseWriter, r *http.Request) {
	username := usernameFromCtx(r)
	if err := s.cdmMgr.Enable(username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.audit != nil {
		s.audit.LogCDMToggle(username, localaudit.InterfaceUI, "disabled", "enabled")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCDMDisable(w http.ResponseWriter, r *http.Request) {
	username := usernameFromCtx(r)
	if err := s.cdmMgr.Disable(username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.audit != nil {
		s.audit.LogCDMToggle(username, localaudit.InterfaceUI, "enabled", "disabled")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCDMGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Duration string `json:"duration"` // e.g. "10m", "1h"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid duration: " + err.Error()})
		return
	}
	if dur <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "duration must be positive"})
		return
	}

	username := usernameFromCtx(r)
	if err := s.cdmMgr.GrantSession(username, dur); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if s.audit != nil {
		expires := time.Now().UTC().Add(dur)
		s.audit.LogCDMGrant(username, localaudit.InterfaceUI, req.Duration, &expires)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCDMRevoke(w http.ResponseWriter, r *http.Request) {
	username := usernameFromCtx(r)
	if err := s.cdmMgr.RevokeSession(username); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if s.audit != nil {
		s.audit.LogCDMRevoke(username, localaudit.InterfaceUI, "user request")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAuditLog(w http.ResponseWriter, _ *http.Request) {
	// Web UI shows the CDM-filtered view (accessible to any authenticated local user).
	if s.audit == nil {
		writeJSON(w, http.StatusOK, []localaudit.Entry{})
		return
	}
	entries, err := s.audit.ReadCDMOnly(100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []localaudit.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
