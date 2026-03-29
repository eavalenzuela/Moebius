package auth

import (
	"net/http"
)

// RequireTenant is middleware that rejects requests where no tenant_id
// has been set in the context (i.e. authentication hasn't run or failed
// to resolve a tenant). This is a safety net — every store query should
// filter by tenant_id, and this middleware ensures it's always present.
func RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if TenantIDFromContext(r.Context()) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{
					"code":    "tenant_required",
					"message": "Request must be associated with a tenant",
				},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
