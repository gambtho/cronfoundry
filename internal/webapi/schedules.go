package webapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/audit"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// runNowClient is a bounded HTTP client for the loopback proxy hop from the
// webapi runNow handler to the internal /run-now endpoint. A timeout keeps a
// hung or slow internal handler from pinning a goroutine.
var runNowClient = &http.Client{Timeout: 30 * time.Second}

// scheduleDTO is the snake_case-keyed wire shape for schedule rows.
// Backs GET /api/schedules — the frontend consumes these exact field names.
type scheduleDTO struct {
	ID              string  `json:"id"`
	SkillID         string  `json:"skill_id"`
	Name            string  `json:"name"`
	Cron            string  `json:"cron"`
	Timezone        string  `json:"timezone"`
	OverlapPolicy   string  `json:"overlap_policy"`
	TimeoutSec      int32   `json:"timeout_sec"`
	Enabled         bool    `json:"enabled"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	NextFireAt      *string `json:"next_fire_at"` // ISO 8601; nil if NULL
	AutoPauseAfter  *int32  `json:"auto_pause_after"`
	AutoPausedAt    *string `json:"auto_paused_at"` // ISO 8601; nil if NULL
	AutoPauseReason *string `json:"auto_pause_reason"`
	LastEnabledAt   string  `json:"last_enabled_at"` // ISO 8601; NOT NULL
	SkillPath       string  `json:"skill_path"`
	SkillName       string  `json:"skill_name"`
	Owner           string  `json:"owner"`
	RepoName        string  `json:"repo_name"`
}

// scheduleRowToDTO converts a sqlc row (pgtype-laden) into the wire shape.
// ISO-formatted timestamps use time.RFC3339Nano for round-trip fidelity.
func scheduleRowToDTO(r dbgen.ListSchedulesByOrgRow) scheduleDTO {
	toISO := func(t pgtype.Timestamptz) *string {
		if !t.Valid {
			return nil
		}
		s := t.Time.Format(time.RFC3339Nano)
		return &s
	}
	return scheduleDTO{
		ID:              uuid.UUID(r.ID.Bytes).String(),
		SkillID:         uuid.UUID(r.SkillID.Bytes).String(),
		Name:            r.Name,
		Cron:            r.Cron,
		Timezone:        r.Timezone,
		OverlapPolicy:   r.OverlapPolicy,
		TimeoutSec:      r.TimeoutSec,
		Enabled:         r.Enabled,
		Provider:        r.Provider,
		Model:           r.Model,
		NextFireAt:      toISO(r.NextFireAt),
		AutoPauseAfter:  r.AutoPauseAfter,
		AutoPausedAt:    toISO(r.AutoPausedAt),
		AutoPauseReason: r.AutoPauseReason,
		LastEnabledAt:   r.LastEnabledAt.Time.Format(time.RFC3339Nano),
		SkillPath:       r.SkillPath,
		SkillName:       r.SkillName,
		Owner:           r.Owner,
		RepoName:        r.RepoName,
	}
}

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
	out := make([]scheduleDTO, len(rows))
	for i, r := range rows {
		out[i] = scheduleRowToDTO(r)
	}
	writeJSON(w, http.StatusOK, out)
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
	idCopy := id // Copy to a local so we can safely take its address for the pointer field.
	action := "schedule.pause"
	if enabled {
		action = "schedule.resume"
	}
	auditLog(r.Context(), h.deps.Queries, mustClaims(r).Login, audit.Entry{
		OrgID:      org.ID,
		Action:     action,
		TargetKind: "schedule",
		TargetID:   &idCopy,
	})
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
		slog.Error("runNow: APIBaseURL not configured — refusing to dial a hardcoded fallback",
			"actor", actor, "schedule_id", idStr)
		writeErr(w, http.StatusServiceUnavailable, "api base url not configured", "config")
		return
	}
	url := apiBase + "/internal/schedules/" + idStr + "/run-now"

	body, _ := json.Marshal(map[string]string{"actor": actor})
	req, err := http.NewRequestWithContext(r.Context(), "POST", url, bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build request", "internal")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := runNowClient.Do(req)
	if err != nil {
		slog.Error("runNow: trigger call failed", "err", err, "schedule_id", idStr)
		writeErr(w, http.StatusBadGateway, "trigger call failed", "gateway")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		writeErr(w, resp.StatusCode, "trigger failed", "trigger_error")
		return
	}
	// Audit is emitted by the internal /run-now endpoint (a single source of
	// truth for schedule.run_now across UI, CLI, and direct internal calls).
	// See internal/api/trigger.go.
	w.WriteHeader(http.StatusAccepted)
}
