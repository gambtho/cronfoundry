package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// ServeHTTP implements GET /internal/repos/{id}/clone-url.
//
// Returns an HTTPS URL with the GitHub App installation token embedded as
// the basic-auth password. Runners use this to shallow-clone via go-git
// without ever seeing the App private key.
func (h cloneURLHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repoID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.deps.Pool)
	row, err := q.GetRepoConnection(r.Context(), pgtype.UUID{Bytes: repoID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "repo not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("load repo: %v", err), http.StatusInternalServerError)
		return
	}

	tok, err := h.deps.Installations.Token(r.Context(), row.GithubAppInstallID)
	if err != nil {
		http.Error(w, "mint install token: "+err.Error(), http.StatusBadGateway)
		return
	}

	url := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", tok, row.Owner, row.Name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": url})
}
