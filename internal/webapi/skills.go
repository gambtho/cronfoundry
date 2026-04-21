package webapi

import "net/http"

type skillsHandler struct{ deps Deps }

func (h *skillsHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	skills, err := h.deps.Queries.ListSkillsByOrg(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list skills", "internal")
		return
	}
	writeJSON(w, http.StatusOK, skills)
}
