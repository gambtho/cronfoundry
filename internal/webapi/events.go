package webapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// runEventDTO mirrors web/src/lib/types.ts:RunEvent. payload_json is emitted
// as raw JSON (not base64) so the SPA can decode it client-side without an
// extra unmarshal step. Empty/missing payloads serialize as null.
type runEventDTO struct {
	ID          int64           `json:"id"`
	RunID       string          `json:"run_id"`
	Ts          string          `json:"ts"`
	Level       string          `json:"level"`
	EventType   string          `json:"event_type"`
	PayloadJSON json.RawMessage `json:"payload_json"`
}

func runEventToDTO(e dbgen.RunEvent) runEventDTO {
	payload := json.RawMessage(e.PayloadJson)
	if len(payload) == 0 {
		payload = json.RawMessage(`null`)
	}
	return runEventDTO{
		ID:          e.ID,
		RunID:       uuidString(e.RunID),
		Ts:          toISO(e.Ts),
		Level:       e.Level,
		EventType:   e.EventType,
		PayloadJSON: payload,
	}
}

type eventsHandler struct{ deps Deps }

func (h *eventsHandler) list(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request")
		return
	}
	events, err := h.deps.Queries.ListRunEvents(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list events", "internal")
		return
	}
	out := make([]runEventDTO, len(events))
	for i, ev := range events {
		out[i] = runEventToDTO(ev)
	}
	writeJSON(w, http.StatusOK, out)
}

// stream sends run events as an SSE stream, polling Postgres every 2s until
// the run reaches a terminal state or the client disconnects.
func (h *eventsHandler) stream(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request")
		return
	}
	pgID := pgtype.UUID{Bytes: id, Valid: true}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported", "internal")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var lastEventID int64
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	terminal := map[string]bool{"succeeded": true, "partial_failure": true, "failed": true}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			events, err := h.deps.Queries.ListRunEventsSince(r.Context(), dbgen.ListRunEventsSinceParams{
				RunID: pgID,
				ID:    lastEventID,
			})
			if err != nil {
				return
			}
			for _, ev := range events {
				// Marshal the DTO (snake_case + tagged) so the SSE client
				// receives the same shape as the polling /api/.../events
				// endpoint. Pre-DTO this marshalled the bare sqlc row and
				// produced PascalCase keys.
				data, _ := json.Marshal(runEventToDTO(ev))
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				lastEventID = ev.ID
			}
			flusher.Flush()

			run, err := h.deps.Queries.GetRun(r.Context(), pgID)
			if err != nil || terminal[run.Status] {
				_, _ = fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}
