package webapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// runSummaryDTO is the wire shape for /api/runs (list). Mirrors
// web/src/lib/types.ts:RunSummary one-for-one.
type runSummaryDTO struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	FireReason   string  `json:"fire_reason"`
	Actor        *string `json:"actor"`
	StartedAt    *string `json:"started_at"`
	FinishedAt   *string `json:"finished_at"`
	DurationMs   *int32  `json:"duration_ms"`
	ErrorKind    *string `json:"error_kind"`
	ErrorMsg     *string `json:"error_msg"`
	CreatedAt    string  `json:"created_at"`
	ScheduleName string  `json:"schedule_name"`
	SkillPath    string  `json:"skill_path"`
	Owner        string  `json:"owner"`
	RepoName     string  `json:"repo_name"`
}

// runDetailDTO extends runSummaryDTO with token + cost + writeback fields
// only present in the detail endpoint. Mirrors RunDetail in types.ts.
type runDetailDTO struct {
	runSummaryDTO
	TokensIn           *int32  `json:"tokens_in"`
	TokensOut          *int32  `json:"tokens_out"`
	CostCents          *int32  `json:"cost_cents"`
	WritebackCommitSha *string `json:"writeback_commit_sha"`
}

func runSummaryRowToDTO(r dbgen.ListRunsForOrgRow) runSummaryDTO {
	return runSummaryDTO{
		ID:           uuidString(r.ID),
		Status:       r.Status,
		FireReason:   r.FireReason,
		Actor:        r.Actor,
		StartedAt:    toISOPtr(r.StartedAt),
		FinishedAt:   toISOPtr(r.FinishedAt),
		DurationMs:   r.DurationMs,
		ErrorKind:    r.ErrorKind,
		ErrorMsg:     r.ErrorMsg,
		CreatedAt:    toISO(r.CreatedAt),
		ScheduleName: r.ScheduleName,
		SkillPath:    r.SkillPath,
		Owner:        r.Owner,
		RepoName:     r.RepoName,
	}
}

func runDetailRowToDTO(r dbgen.GetRunForAdminRow) runDetailDTO {
	return runDetailDTO{
		runSummaryDTO: runSummaryDTO{
			ID:           uuidString(r.ID),
			Status:       r.Status,
			FireReason:   r.FireReason,
			Actor:        r.Actor,
			StartedAt:    toISOPtr(r.StartedAt),
			FinishedAt:   toISOPtr(r.FinishedAt),
			DurationMs:   r.DurationMs,
			ErrorKind:    r.ErrorKind,
			ErrorMsg:     r.ErrorMsg,
			CreatedAt:    toISO(r.CreatedAt),
			ScheduleName: r.ScheduleName,
			SkillPath:    r.SkillPath,
			Owner:        r.Owner,
			RepoName:     r.RepoName,
		},
		TokensIn:           r.TokensIn,
		TokensOut:          r.TokensOut,
		CostCents:          r.CostCents,
		WritebackCommitSha: r.WritebackCommitSha,
	}
}

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
	rows, err := h.deps.Queries.ListRunsForOrg(r.Context(), dbgen.ListRunsForOrgParams{
		OrgID: org.ID,
		Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list runs", "internal")
		return
	}
	out := make([]runSummaryDTO, len(rows))
	for i, row := range rows {
		out[i] = runSummaryRowToDTO(row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *runsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request")
		return
	}
	row, err := h.deps.Queries.GetRunForAdmin(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "run not found", "not_found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load run", "internal")
		return
	}
	writeJSON(w, http.StatusOK, runDetailRowToDTO(row))
}
