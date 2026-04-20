package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

var validFinalizeStatuses = map[string]bool{
	"succeeded":       true,
	"partial_failure": true,
	"failed":          true,
}

type finalizeBody struct {
	Status             string  `json:"status"`
	DurationMs         *int32  `json:"duration_ms,omitempty"`
	TokensIn           *int32  `json:"tokens_in,omitempty"`
	TokensOut          *int32  `json:"tokens_out,omitempty"`
	CostCents          *int32  `json:"cost_cents,omitempty"`
	ErrorKind          *string `json:"error_kind,omitempty"`
	ErrorMsg           *string `json:"error_msg,omitempty"`
	WritebackCommitSha *string `json:"writeback_commit_sha,omitempty"`
}

// ServeHTTP implements POST /internal/runs/{id}/finalize.
// Body carries final status + accounting + optional writeback SHA + optional
// error kind/msg. Returns 204 on success.
func (h finalizeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	if ClaimsFromContext(r.Context()).RunID != urlRunID {
		http.Error(w, "token run_id mismatch", http.StatusForbidden)
		return
	}

	var body finalizeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !validFinalizeStatuses[body.Status] {
		http.Error(w, "invalid status: "+body.Status, http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.deps.Pool)
	if _, err := q.FinalizeRun(r.Context(), dbgen.FinalizeRunParams{
		ID:                 pgtype.UUID{Bytes: urlRunID, Valid: true},
		Status:             body.Status,
		DurationMs:         body.DurationMs,
		TokensIn:           body.TokensIn,
		TokensOut:          body.TokensOut,
		CostCents:          body.CostCents,
		ErrorKind:          body.ErrorKind,
		ErrorMsg:           body.ErrorMsg,
		WritebackCommitSha: body.WritebackCommitSha,
	}); err != nil {
		http.Error(w, "finalize: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
