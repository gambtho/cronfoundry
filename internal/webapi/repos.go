package webapi

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type reposHandler struct{ deps Deps }

func (h *reposHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	repos, err := h.deps.Queries.ListRepoConnections(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list repos", "internal")
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

type connectRepoRequest struct {
	InstallID     int64  `json:"install_id"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

func (h *reposHandler) connect(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	var req connectRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body", "bad_request")
		return
	}
	if req.Owner == "" || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "owner and name are required", "bad_request")
		return
	}
	branch := req.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	repo, err := h.deps.Queries.InsertRepoConnection(r.Context(), dbgen.InsertRepoConnectionParams{
		OrgID:              org.ID,
		GithubAppInstallID: req.InstallID,
		Owner:              req.Owner,
		Name:               req.Name,
		DefaultBranch:      branch,
		SyncIntervalSec:    300,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to connect repo", "internal")
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (h *reposHandler) disconnect(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid repo id", "bad_request")
		return
	}
	n, err := h.deps.Queries.DeleteRepoConnection(r.Context(), dbgen.DeleteRepoConnectionParams{
		ID:    pgtype.UUID{Bytes: id, Valid: true},
		OrgID: org.ID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete repo", "internal")
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "repo not found", "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
