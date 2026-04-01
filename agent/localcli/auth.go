package localcli

import (
	"context"
	"encoding/json"

	"github.com/eavalenzuela/Moebius/agent/ipc"
	"github.com/eavalenzuela/Moebius/agent/localauth"
)

// RequireAuthMiddleware returns a wrapper that checks the IPC request's
// session token before invoking the handler. If the token is missing or
// invalid, it returns CodeUnauthorized.
func RequireAuthMiddleware(sessions *localauth.SessionManager) func(ipc.HandlerFunc) ipc.HandlerFunc {
	return func(next ipc.HandlerFunc) ipc.HandlerFunc {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			token := ipc.TokenFromContext(ctx)
			if token == "" {
				return nil, &ipc.Error{Code: ipc.CodeUnauthorized, Message: "authentication required"}
			}
			if _, err := sessions.Validate(token); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeUnauthorized, Message: "invalid or expired session"}
			}
			return next(ctx, params)
		}
	}
}
