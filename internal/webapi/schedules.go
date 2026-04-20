package webapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type schedulesHandler struct{ deps Deps }

func (h *schedulesHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	rows, err := h.deps.Queries.ListSchedulesByOrg(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list schedules", "internal")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *schedulesHandler) pause(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

func (h *schedulesHandler) resume(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true)
}

func (h *schedulesHandler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid schedule id", "bad_request")
		return
	}
	sched, err := h.deps.Queries.SetScheduleEnabled(r.Context(), dbgen.SetScheduleEnabledParams{
		ID:      pgtype.UUID{Bytes: id, Valid: true},
		Enabled: enabled,
		OrgID:   org.ID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update schedule", "internal")
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

func (h *schedulesHandler) runNow(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if _, err := uuid.Parse(idStr); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid schedule id", "bad_request")
		return
	}
	claims := mustClaims(r)
	actor := claims.Login

	apiBase := h.deps.APIBaseURL
	if apiBase == "" {
		apiBase = "http://127.0.0.1:8080"
	}
	url := apiBase + "/internal/schedules/" + idStr + "/run-now"

	body, _ := json.Marshal(map[string]string{"actor": actor})
	req, err := http.NewRequestWithContext(r.Context(), "POST", url, bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build request", "internal")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "trigger call failed", "gateway")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeErr(w, resp.StatusCode, "trigger failed", "trigger_error")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
