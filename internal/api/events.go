package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type eventBatch struct {
	Events []struct {
		Type    string          `json:"type"`
		Level   string          `json:"level"`
		Payload json.RawMessage `json:"payload"`
	} `json:"events"`
}

// ServeHTTP implements POST /internal/runs/{id}/events.
// Body: {"events": [{"type": "...", "level": "info|warn|error", "payload": {...}}]}.
// Empty level defaults to "info". 204 on success.
//
// The batch is inserted within a single transaction: either every event in
// the request lands or none do. A mid-batch validation or DB error rolls
// the transaction back, so the caller can safely retry the full batch.
func (h eventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	if ClaimsFromContext(r.Context()).RunID != urlRunID {
		http.Error(w, "token run_id mismatch", http.StatusForbidden)
		return
	}

	var batch eventBatch
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	tx, err := h.deps.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "begin tx: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(r.Context()) }()

	q := dbgen.New(tx)
	for _, ev := range batch.Events {
		level := ev.Level
		if level == "" {
			level = "info"
		}
		if level != "info" && level != "warn" && level != "error" {
			http.Error(w, "invalid level: "+level, http.StatusBadRequest)
			return
		}
		payload := ev.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		if err := q.InsertRunEvent(r.Context(), dbgen.InsertRunEventParams{
			RunID:       pgtype.UUID{Bytes: urlRunID, Valid: true},
			Level:       level,
			EventType:   ev.Type,
			PayloadJson: payload,
		}); err != nil {
			http.Error(w, "persist: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "commit: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
