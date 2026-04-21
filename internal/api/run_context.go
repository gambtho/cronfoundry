package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/config"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// RunContext is the JSON returned to the runner. Field names are snake_case
// to match the rest of the /internal surface.
type RunContext struct {
	RunID           string          `json:"run_id"`
	OrgID           string          `json:"org_id"`
	ScheduleName    string          `json:"schedule_name"`
	SkillPath       string          `json:"skill_path"`
	SkillSha        string          `json:"skill_sha"`
	Repo            string          `json:"repo"` // "owner/name"
	RepoID          string          `json:"repo_id"`
	DefaultBranch   string          `json:"default_branch"`
	InstallationID  int64           `json:"installation_id"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	LLMSecretRef    *string         `json:"llm_secret_ref,omitempty"`
	LLMEndpoint     *string         `json:"llm_endpoint,omitempty"`
	LLMDeployment   *string         `json:"llm_deployment,omitempty"`
	Destinations    json.RawMessage `json:"destinations"`
	Writeback       json.RawMessage `json:"writeback,omitempty"`
	Env             json.RawMessage `json:"env"`
	FrontmatterJSON json.RawMessage `json:"frontmatter"`
	// SecretManifest lists the names of secrets this run is allowed to fetch;
	// derived from destinations/env/llm_secret_ref.
	SecretManifest []string `json:"secret_manifest"`
}

// ServeHTTP implements the GET /internal/runs/{id}/context endpoint.
//
// Authorization: requireBearer middleware has already verified the JWT.
// This handler additionally enforces that the URL's run_id matches the
// claim's RunID — so a runner can only fetch its own context, not another
// run's.
func (h runContextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}

	claims := ClaimsFromContext(r.Context())
	if claims.RunID != urlRunID {
		http.Error(w, "token run_id does not match URL", http.StatusForbidden)
		return
	}

	q := dbgen.New(h.deps.Pool)
	row, err := q.GetRunForContext(r.Context(), pgtype.UUID{Bytes: urlRunID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("load run: %v", err), http.StatusInternalServerError)
		return
	}

	out := RunContext{
		RunID:           urlRunID.String(),
		OrgID:           uuid.UUID(row.OrgID.Bytes).String(),
		ScheduleName:    row.ScheduleName,
		SkillPath:       row.SkillPath,
		SkillSha:        row.SkillSha,
		Repo:            row.Owner + "/" + row.RepoName,
		RepoID:          uuid.UUID(row.SkillRepoID.Bytes).String(),
		DefaultBranch:   row.DefaultBranch,
		InstallationID:  row.GithubAppInstallID,
		Provider:        row.Provider,
		Model:           row.Model,
		LLMSecretRef:    row.LlmSecretRef,
		LLMEndpoint:     row.LlmEndpoint,
		LLMDeployment:   row.LlmDeployment,
		Destinations:    row.DestinationsJson,
		Writeback:       row.WritebackJson,
		Env:             row.EnvJson,
		FrontmatterJSON: row.FrontmatterJson,
	}
	out.SecretManifest = config.CollectSecretRefs(row.DestinationsJson, row.EnvJson, row.LlmSecretRef)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		// Body already partially written; nothing to recover. The API's
		// slog pipeline will observe this via stdlib log if enabled.
		_ = err
	}
}
