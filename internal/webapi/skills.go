package webapi

import (
	"net/http"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// skillDTO mirrors web/src/lib/types.ts:Skill. The Org ID and the raw
// frontmatter JSON columns from the sqlc row are intentionally omitted —
// they're internal concerns the SPA doesn't render.
type skillDTO struct {
	ID         string `json:"id"`
	RepoID     string `json:"repo_id"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	CurrentSha string `json:"current_sha"`
	UpdatedAt  string `json:"updated_at"`
	Owner      string `json:"owner"`
	RepoName   string `json:"repo_name"`
}

func skillRowToDTO(r dbgen.ListSkillsByOrgRow) skillDTO {
	return skillDTO{
		ID:         uuidString(r.ID),
		RepoID:     uuidString(r.RepoID),
		Path:       r.Path,
		Name:       r.Name,
		CurrentSha: r.CurrentSha,
		UpdatedAt:  toISO(r.UpdatedAt),
		Owner:      r.Owner,
		RepoName:   r.RepoName,
	}
}

type skillsHandler struct{ deps Deps }

func (h *skillsHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	rows, err := h.deps.Queries.ListSkillsByOrg(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list skills", "internal")
		return
	}
	out := make([]skillDTO, len(rows))
	for i, row := range rows {
		out[i] = skillRowToDTO(row)
	}
	writeJSON(w, http.StatusOK, out)
}
