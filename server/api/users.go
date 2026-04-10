package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// UsersHandler handles /v1/users endpoints.
type UsersHandler struct {
	store *store.Store
	audit *audit.Logger
}

// NewUsersHandler creates a UsersHandler.
func NewUsersHandler(s *store.Store, auditLog *audit.Logger) *UsersHandler {
	return &UsersHandler{store: s, audit: auditLog}
}

// List handles GET /v1/users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	limit, cursor := ParsePagination(r)

	users, err := h.store.ListUsers(r.Context(), tenantID, cursor, limit)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	hasMore := len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	var nextCursor string
	if hasMore && len(users) > 0 {
		nextCursor = users[len(users)-1].ID
	}

	JSON(w, http.StatusOK, ListResponse{
		Data: users,
		Pagination: Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			Limit:      limit,
		},
	})
}

// Get handles GET /v1/users/{user_id}.
func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	userID := chi.URLParam(r, "user_id")

	user, err := h.store.GetUser(r.Context(), tenantID, userID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user == nil {
		ErrorWithCode(w, http.StatusNotFound, "user_not_found", "No user with the given ID exists")
		return
	}
	JSON(w, http.StatusOK, user)
}

type inviteUserRequest struct {
	Email  string `json:"email"`
	RoleID string `json:"role_id"`
}

// Invite handles POST /v1/users/invite.
func (h *UsersHandler) Invite(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	actorID := auth.UserIDFromContext(r.Context())

	var req inviteUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.RoleID == "" {
		Error(w, http.StatusBadRequest, "email and role_id are required")
		return
	}

	user := &models.User{
		ID:        models.NewUserID(),
		TenantID:  tenantID,
		Email:     req.Email,
		RoleID:    &req.RoleID,
		CreatedAt: time.Now().UTC(),
	}

	if err := h.store.CreateUser(r.Context(), user); err != nil {
		Error(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, actorID, models.ActorTypeUser,
			"user.invite", "user", user.ID, map[string]string{
				"email":   req.Email,
				"role_id": req.RoleID,
			})
	}

	JSON(w, http.StatusCreated, user)
}

type updateUserRequest struct {
	RoleID string `json:"role_id"`
}

// Update handles PATCH /v1/users/{user_id}.
func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	actorID := auth.UserIDFromContext(r.Context())
	userID := chi.URLParam(r, "user_id")

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RoleID == "" {
		Error(w, http.StatusBadRequest, "role_id is required")
		return
	}

	// Self-promotion guard: a non-admin caller cannot edit their own role
	// at all (changing it always carries escalation risk — even "demoting"
	// could remove a sibling check), and cannot assign a role whose
	// permission set exceeds the caller's own. Admins bypass both.
	if !auth.IsAdminFromContext(r.Context()) {
		if userID == actorID {
			ErrorWithCode(w, http.StatusForbidden, "self_role_change",
				"cannot change your own role")
			return
		}
		targetRole, err := h.store.GetRole(r.Context(), tenantID, req.RoleID)
		if err != nil {
			Error(w, http.StatusInternalServerError, "failed to load target role")
			return
		}
		if targetRole == nil {
			ErrorWithCode(w, http.StatusBadRequest, "role_not_found",
				"the requested role does not exist in this tenant")
			return
		}
		callerPerms := auth.PermissionsFromContext(r.Context())
		if !auth.PermissionsSubset(callerPerms, targetRole.Permissions) {
			ErrorWithCode(w, http.StatusForbidden, "permission_escalation",
				"cannot assign a role with more permissions than your own")
			return
		}
	}

	if err := h.store.UpdateUserRole(r.Context(), tenantID, userID, req.RoleID); err != nil {
		ErrorWithCode(w, http.StatusNotFound, "user_not_found", err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, actorID, models.ActorTypeUser,
			"user.update_role", "user", userID, map[string]string{
				"role_id": req.RoleID,
			})
	}

	user, _ := h.store.GetUser(r.Context(), tenantID, userID)
	JSON(w, http.StatusOK, user)
}

type setSSOSubjectRequest struct {
	SSOSubject string `json:"sso_subject"`
}

// SetSSOSubject handles PUT /v1/users/{user_id}/sso-subject.
//
// Links a Moebius user to an OIDC `sub` claim so that an SSO login
// resolves to this user. An empty body unlinks. Required because the
// OIDC middleware looks up users via `users.sso_subject`, and no
// JIT-provisioning path exists — an operator must explicitly bind
// each user. The partial unique index on `users.sso_subject` enforces
// one-subject-per-user at the DB level; a duplicate returns 409.
func (h *UsersHandler) SetSSOSubject(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	actorID := auth.UserIDFromContext(r.Context())
	userID := chi.URLParam(r, "user_id")

	var req setSSOSubjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.SSOSubject = strings.TrimSpace(req.SSOSubject)

	if err := h.store.SetUserSSOSubject(r.Context(), tenantID, userID, req.SSOSubject); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Unique-violation on users_sso_subject_unique — another
			// user in some tenant is already linked to this subject.
			ErrorWithCode(w, http.StatusConflict, "sso_subject_taken",
				"another user is already linked to this SSO subject")
			return
		}
		if err.Error() == "user not found" {
			ErrorWithCode(w, http.StatusNotFound, "user_not_found",
				"No user with the given ID exists")
			return
		}
		Error(w, http.StatusInternalServerError, "failed to set sso subject")
		return
	}

	if h.audit != nil {
		action := "user.link_sso"
		meta := map[string]string{"sso_subject": req.SSOSubject}
		if req.SSOSubject == "" {
			action = "user.unlink_sso"
			meta = nil
		}
		h.audit.LogAction(r.Context(), tenantID, actorID, models.ActorTypeUser,
			action, "user", userID, meta)
	}

	w.WriteHeader(http.StatusNoContent)
}

// Deactivate handles POST /v1/users/{user_id}/deactivate.
func (h *UsersHandler) Deactivate(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	actorID := auth.UserIDFromContext(r.Context())
	userID := chi.URLParam(r, "user_id")

	if err := h.store.DeactivateUser(r.Context(), tenantID, userID); err != nil {
		ErrorWithCode(w, http.StatusNotFound, "user_not_found", err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), tenantID, actorID, models.ActorTypeUser,
			"user.deactivate", "user", userID, nil)
	}

	w.WriteHeader(http.StatusNoContent)
}
