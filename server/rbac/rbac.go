package rbac

import (
	"net/http"

	"github.com/moebius-oss/moebius/server/auth"
)

// Require returns middleware that checks the authenticated user has the
// given permission. If the user is an admin (is_admin flag on API key),
// the check is bypassed.
func Require(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Admins bypass permission checks
			if auth.IsAdminFromContext(ctx) {
				next.ServeHTTP(w, r)
				return
			}

			perms := auth.PermissionsFromContext(ctx)
			if !hasPermission(perms, permission) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"You do not have permission to perform this action"}}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func hasPermission(perms []string, required string) bool {
	for _, p := range perms {
		if p == required {
			return true
		}
	}
	return false
}
