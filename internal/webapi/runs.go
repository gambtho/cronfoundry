package webapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type runsHandler struct{ deps Deps }

func (h *runsHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	limit := int32(50)
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	runs, err := h.deps.Queries.ListRunsForOrg(r.Context(), dbgen.ListRunsForOrgParams{
		OrgID: org.ID,
		Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list runs", "internal")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *runsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request")
		return
	}
	run, err := h.deps.Queries.GetRunForAdmin(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		writeErr(w, http.StatusNotFound, "run not found", "not_found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}
