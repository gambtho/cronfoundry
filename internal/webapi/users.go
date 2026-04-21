package webapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/gambtho/cronfoundry/internal/audit"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type usersHandler struct{ deps Deps }

// userRequest is the body for POST /api/users and PATCH /api/users/{login}.
// For POST both fields are required; for PATCH only role is used.
type userRequest struct {
	Login string `json:"login"`
	Role  string `json:"role"`
}

// userDTO is the stable JSON shape returned by list/create. Flattens
// nullable timestamp to "" for absent, role-limited to admin|viewer.
type userDTO struct {
	Login       string `json:"login"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at,omitempty"`
}

func toUserDTO(row dbgen.AppUser) userDTO {
	d := userDTO{
		Login: row.GithubLogin,
		Role:  row.Role,
	}
	if row.CreatedAt.Valid {
		d.CreatedAt = row.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if row.LastLoginAt.Valid {
		d.LastLoginAt = row.LastLoginAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return d
}

func validRole(role string) bool {
	return role == "admin" || role == "viewer"
}

// list implements GET /api/users. Returns every user in the single-tenant
// org, sorted by github_login.
func (h *usersHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	rows, err := h.deps.Queries.ListUsers(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list users", "internal")
		return
	}
	out := make([]userDTO, len(rows))
	for i, row := range rows {
		out[i] = toUserDTO(row)
	}
	writeJSON(w, http.StatusOK, out)
}

// create implements POST /api/users. Requires login + role. Returns 409
// if the user already exists (the sqlc query uses ON CONFLICT DO NOTHING,
// which surfaces as pgx.ErrNoRows on Scan).
func (h *usersHandler) create(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Login == "" || !validRole(req.Role) {
		writeErr(w, http.StatusBadRequest, "login and role (admin|viewer) are required", "bad_request")
		return
	}
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	row, err := h.deps.Queries.CreateUser(r.Context(), dbgen.CreateUserParams{
		OrgID:       org.ID,
		GithubLogin: req.Login,
		Role:        req.Role,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusConflict, "user already exists", "conflict")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to create user", "internal")
		return
	}
	auditLog(r.Context(), h.deps.Queries, mustClaims(r).Login, audit.Entry{
		OrgID:      org.ID,
		Action:     "user.create",
		TargetKind: "user",
		Detail:     map[string]any{"login": req.Login, "role": req.Role},
	})
	writeJSON(w, http.StatusCreated, toUserDTO(row))
}

// updateRole implements PATCH /api/users/{login}. Body: {"role": "admin"|"viewer"}.
// 404 if the user doesn't exist.
func (h *usersHandler) updateRole(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	if login == "" {
		writeErr(w, http.StatusBadRequest, "login is required", "bad_request")
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !validRole(req.Role) {
		writeErr(w, http.StatusBadRequest, "role must be admin or viewer", "bad_request")
		return
	}
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	// Verify the user exists first — SetUserRole is a blind UPDATE and
	// won't error when the row is missing.
	if _, err := h.deps.Queries.GetUserRole(r.Context(), dbgen.GetUserRoleParams{
		OrgID:       org.ID,
		GithubLogin: login,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "user not found", "not_found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load user", "internal")
		return
	}
	if err := h.deps.Queries.SetUserRole(r.Context(), dbgen.SetUserRoleParams{
		OrgID:       org.ID,
		GithubLogin: login,
		Role:        req.Role,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update role", "internal")
		return
	}
	auditLog(r.Context(), h.deps.Queries, mustClaims(r).Login, audit.Entry{
		OrgID:      org.ID,
		Action:     "user.set_role",
		TargetKind: "user",
		Detail:     map[string]any{"login": login, "role": req.Role},
	})
	w.WriteHeader(http.StatusNoContent)
}

// delete implements DELETE /api/users/{login}. Guards: can't delete
// yourself (409 self_delete), can't delete the last user (409 last_user).
func (h *usersHandler) delete(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	if login == "" {
		writeErr(w, http.StatusBadRequest, "login is required", "bad_request")
		return
	}
	if mustClaims(r).Login == login {
		writeErr(w, http.StatusConflict, "cannot delete your own user", "self_delete")
		return
	}
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	count, err := h.deps.Queries.CountUsers(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to count users", "internal")
		return
	}
	if count <= 1 {
		writeErr(w, http.StatusConflict, "cannot delete the last user", "last_user")
		return
	}
	affected, err := h.deps.Queries.DeleteUser(r.Context(), dbgen.DeleteUserParams{
		OrgID:       org.ID,
		GithubLogin: login,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete user", "internal")
		return
	}
	if affected == 0 {
		writeErr(w, http.StatusNotFound, "user not found", "not_found")
		return
	}
	auditLog(r.Context(), h.deps.Queries, mustClaims(r).Login, audit.Entry{
		OrgID:      org.ID,
		Action:     "user.delete",
		TargetKind: "user",
		Detail:     map[string]any{"login": login},
	})
	w.WriteHeader(http.StatusNoContent)
}
