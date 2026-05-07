package webapi

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/scheduler"
)

type alertsDTO struct {
	QuietJobs       []quietJobDTO `json:"quiet_jobs"`
	RecentlyPaused  []pausedDTO   `json:"recently_paused"`
	ExpiringSecrets []struct{}    `json:"expiring_secrets"`
	Drift           []struct{}    `json:"drift"`
}

type quietJobDTO struct {
	ScheduleID    string  `json:"schedule_id"`
	ScheduleName  string  `json:"schedule_name"`
	LastSuccess   *string `json:"last_success"`
	ExpectedEvery int64   `json:"expected_every"` // seconds
}

type pausedDTO struct {
	ScheduleID   string  `json:"schedule_id"`
	ScheduleName string  `json:"schedule_name"`
	PausedAt     string  `json:"paused_at"`
	Reason       *string `json:"reason"`
}

type alertsHandler struct {
	deps Deps
	now  func() time.Time // injectable for tests
}

func newAlertsHandler(deps Deps) *alertsHandler {
	return &alertsHandler{deps: deps, now: time.Now}
}

func (h *alertsHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load org", "internal")
		return
	}

	cands, err := h.deps.Queries.ListSchedulesForQuietCheck(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "alerts: quiet candidates", "internal")
		return
	}

	quiet := make([]quietJobDTO, 0)
	now := h.now()
	for _, c := range cands {
		interval, ok := expectedInterval(c.Cron, c.Timezone, now)
		if !ok {
			continue // bad cron -> skip silently; not an alert source
		}
		threshold := 3 * interval
		if threshold < time.Hour {
			threshold = time.Hour
		}

		var lastISO *string
		var lastT time.Time
		if c.LastSuccess.Valid {
			lastT = c.LastSuccess.Time
			iso := lastT.UTC().Format(time.RFC3339)
			lastISO = &iso
		}
		isQuiet := !c.LastSuccess.Valid || now.Sub(lastT) > threshold
		if !isQuiet {
			continue
		}
		quiet = append(quiet, quietJobDTO{
			ScheduleID:    uuidString(c.ID),
			ScheduleName:  c.Name,
			LastSuccess:   lastISO,
			ExpectedEvery: int64(interval / time.Second),
		})
		if len(quiet) >= 20 {
			break
		}
	}

	paused, err := h.deps.Queries.ListRecentAutoPaused(r.Context(), dbgen.ListRecentAutoPausedParams{
		OrgID:  org.ID,
		Cutoff: pgtype.Timestamptz{Time: now.Add(-7 * 24 * time.Hour), Valid: true},
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "alerts: paused", "internal")
		return
	}
	pausedOut := make([]pausedDTO, len(paused))
	for i, p := range paused {
		pausedOut[i] = pausedDTO{
			ScheduleID:   uuidString(p.ID),
			ScheduleName: p.Name,
			PausedAt:     p.AutoPausedAt.Time.UTC().Format(time.RFC3339),
			Reason:       p.AutoPauseReason,
		}
	}

	writeJSON(w, http.StatusOK, alertsDTO{
		QuietJobs:       quiet,
		RecentlyPaused:  pausedOut,
		ExpiringSecrets: []struct{}{},
		Drift:           []struct{}{},
	})
}

// expectedInterval estimates the typical step between consecutive
// fires of a cron expression by computing two consecutive nexts and
// taking the difference. Returns (0, false) on parse error.
func expectedInterval(expr, tz string, now time.Time) (time.Duration, bool) {
	t1, err := scheduler.NextFire(expr, tz, now)
	if err != nil {
		return 0, false
	}
	t2, err := scheduler.NextFire(expr, tz, t1)
	if err != nil {
		return 0, false
	}
	return t2.Sub(t1), true
}
