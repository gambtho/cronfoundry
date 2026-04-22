package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
// error kind/msg.
//
// Returns:
//   - 204 on success
//   - 400 for invalid status, invalid body, or negative accounting values
//   - 403 if the bearer's run_id doesn't match the URL
//   - 409 if the run is already terminal (a crashed-then-restarted runner
//     can't clobber the original finalization)
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
	// Accounting fields are nullable (runner may not have useful values for
	// every field), but when supplied they must be non-negative — a negative
	// token count or cost indicates a bug in the runner's instrumentation
	// and should not silently pollute analytics.
	if body.DurationMs != nil && *body.DurationMs < 0 {
		http.Error(w, "duration_ms must be non-negative", http.StatusBadRequest)
		return
	}
	if body.TokensIn != nil && *body.TokensIn < 0 {
		http.Error(w, "tokens_in must be non-negative", http.StatusBadRequest)
		return
	}
	if body.TokensOut != nil && *body.TokensOut < 0 {
		http.Error(w, "tokens_out must be non-negative", http.StatusBadRequest)
		return
	}
	if body.CostCents != nil && *body.CostCents < 0 {
		http.Error(w, "cost_cents must be non-negative", http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.deps.Pool)
	row, err := q.FinalizeRun(r.Context(), dbgen.FinalizeRunParams{
		ID:                 pgtype.UUID{Bytes: urlRunID, Valid: true},
		Status:             body.Status,
		DurationMs:         body.DurationMs,
		TokensIn:           body.TokensIn,
		TokensOut:          body.TokensOut,
		CostCents:          body.CostCents,
		ErrorKind:          body.ErrorKind,
		ErrorMsg:           body.ErrorMsg,
		WritebackCommitSha: body.WritebackCommitSha,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The FinalizeRun query's WHERE clause refuses to match runs
			// already in a terminal state. A missing run_id would also
			// produce ErrNoRows — but the URL-vs-claims guard above combined
			// with the token-hash validation in requireBearer means the
			// only realistic path here is a double-finalize attempt. 409
			// tells the caller not to retry.
			http.Error(w, "run already terminal", http.StatusConflict)
			return
		}
		http.Error(w, "finalize: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Auto-pause evaluation runs AFTER finalize has committed, in its own
	// transaction. Errors here are logged and swallowed — the finalize
	// response must not depend on it.
	scheduleUUID := uuid.UUID(row.ScheduleID.Bytes)
	if err := evaluateAutoPause(
		r.Context(), h.deps.Pool,
		scheduleUUID, urlRunID,
		body.Status, row.FireReason,
	); err != nil {
		slog.Warn("finalize: auto-pause evaluation failed (non-fatal)",
			"run_id", urlRunID, "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
}
