package webapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// runNotificationDTO mirrors web/src/lib/types.ts:RunNotification. Reason
// serializes as null when the underlying column is NULL — sqlc generates
// *string for nullable text, which encoding/json renders the way the
// frontend expects.
type runNotificationDTO struct {
	ID        int64   `json:"id"`
	RunID     string  `json:"run_id"`
	Kind      string  `json:"kind"`
	Target    string  `json:"target"`
	Status    string  `json:"status"`
	Reason    *string `json:"reason"`
	CreatedAt string  `json:"created_at"`
}

func runNotificationToDTO(n dbgen.ListRunNotificationsRow) runNotificationDTO {
	return runNotificationDTO{
		ID:        n.ID,
		RunID:     uuidString(n.RunID),
		Kind:      n.Kind,
		Target:    n.Target,
		Status:    n.Status,
		Reason:    n.Reason,
		CreatedAt: toISO(n.CreatedAt),
	}
}

type runNotificationsHandler struct{ deps Deps }

func (h *runNotificationsHandler) list(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request")
		return
	}
	rows, err := h.deps.Queries.ListRunNotifications(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list notifications", "internal")
		return
	}
	out := make([]runNotificationDTO, len(rows))
	for i, n := range rows {
		out[i] = runNotificationToDTO(n)
	}
	writeJSON(w, http.StatusOK, out)
}
