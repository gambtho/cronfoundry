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
//
// Access is scoped to the authenticated run's own repo: the handler looks
// up run.schedule.skill.repo_id via the run_id in the bearer claims and
// rejects with 403 if the requested repo_id doesn't match. This prevents
// any runner bearer from fetching an arbitrary repo's clone URL.
func (h cloneURLHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repoID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}

	// smokeCloneOverride is a compile-time no-op in production builds
	// (clone_url_prod.go). In e2e builds (clone_url_e2e.go) it reads
	// CRONFOUNDRY_SMOKE_CLONE_URL so the smoke harness can inject a local URL.
	if v, ok := smokeCloneOverride(); ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": v})
		return
	}

	// Scope check: the caller may only fetch the clone URL for the repo
	// their authenticated run is bound to.
	runID := ClaimsFromContext(r.Context()).RunID
	q := dbgen.New(h.deps.Pool)
	scopedRepoID, err := q.GetRunRepoID(r.Context(), pgtype.UUID{Bytes: runID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "run not found", http.StatusForbidden)
			return
		}
		http.Error(w, "scope check: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if uuid.UUID(scopedRepoID.Bytes) != repoID {
		http.Error(w, "repo not in run scope", http.StatusForbidden)
		return
	}

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
