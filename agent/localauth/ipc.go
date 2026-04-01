package localauth

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eavalenzuela/Moebius/agent/ipc"
	"github.com/eavalenzuela/Moebius/agent/localaudit"
)

// LoginParams is the request body for "auth.login".
type LoginParams struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResult is the response body for "auth.login".
type LoginResult struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// ValidateParams is the request body for "auth.validate".
type ValidateParams struct {
	Token string `json:"token"`
}

// ValidateResult is the response body for "auth.validate".
type ValidateResult struct {
	Username string `json:"username"`
	Valid    bool   `json:"valid"`
}

// RegisterIPC registers authentication IPC methods on the router:
//   - auth.login  — validate credentials, return a CLI session token
//   - auth.validate — check if a token is still valid
//   - auth.logout — revoke a session token
//
// If audit is non-nil, auth success/failure events are logged.
func RegisterIPC(router *ipc.Router, auth Authenticator, sessions *SessionManager, audit ...*localaudit.Logger) {
	var auditLog *localaudit.Logger
	if len(audit) > 0 {
		auditLog = audit[0]
	}

	router.Handle("auth.login", func(_ context.Context, params json.RawMessage) (any, error) {
		var p LoginParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "invalid params: " + err.Error()}
		}
		if p.Username == "" || p.Password == "" {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "username and password required"}
		}

		if err := auth.Authenticate(p.Username, p.Password); err != nil {
			if auditLog != nil {
				auditLog.LogAuthFailure(p.Username, localaudit.InterfaceCLI, "invalid credentials")
			}
			return nil, &ipc.Error{Code: ipc.CodeUnauthorized, Message: fmt.Sprintf("authentication failed for %s", p.Username)}
		}

		sess, err := sessions.Create(p.Username, SessionCLI)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInternal, Message: "create session: " + err.Error()}
		}

		if auditLog != nil {
			auditLog.LogAuthSuccess(p.Username, localaudit.InterfaceCLI)
		}

		return LoginResult{
			Token:     sess.Token,
			ExpiresAt: sess.ExpiresAt.Format("2006-01-02T15:04:05Z"),
		}, nil
	})

	router.Handle("auth.validate", func(_ context.Context, params json.RawMessage) (any, error) {
		var p ValidateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "invalid params: " + err.Error()}
		}

		sess, err := sessions.Validate(p.Token)
		if err != nil {
			return ValidateResult{Valid: false}, nil
		}

		return ValidateResult{
			Username: sess.Username,
			Valid:    true,
		}, nil
	})

	router.Handle("auth.logout", func(_ context.Context, params json.RawMessage) (any, error) {
		var p ValidateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeInvalidParams, Message: "invalid params: " + err.Error()}
		}

		sessions.Revoke(p.Token)
		return map[string]bool{"ok": true}, nil
	})
}
