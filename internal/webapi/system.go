package webapi

import (
	"net/http"
	"time"
)

type systemHealthDTO struct {
	Scheduler  schedulerDTO `json:"scheduler"`
	QueueDepth int64        `json:"queue_depth"`
	Workers    int64        `json:"workers"`
	LastSyncAt *string      `json:"last_sync_at"`
}

type schedulerDTO struct {
	Status     string  `json:"status"`       // healthy | degraded | down
	LastTickAt *string `json:"last_tick_at"` // ISO or null
}

type systemHandler struct{ deps Deps }

func (h *systemHandler) health(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load org", "internal")
		return
	}

	qd, err := h.deps.Queries.CountQueueDepth(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "queue", "internal")
		return
	}
	workers, err := h.deps.Queries.CountActiveWorkers(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "workers", "internal")
		return
	}
	last, err := h.deps.Queries.LastRunCreatedAt(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "last sync", "internal")
		return
	}

	var lastTick *string
	sStatus := "down"
	if h.deps.Clock != nil {
		t := h.deps.Clock.LastTickAt()
		if !t.IsZero() {
			iso := t.UTC().Format(time.RFC3339)
			lastTick = &iso
			age := time.Since(t)
			interval := h.deps.SweepInterval
			if interval <= 0 {
				interval = 30 * time.Second
			}
			switch {
			case age < 2*interval:
				sStatus = "healthy"
			case age < 5*interval:
				sStatus = "degraded"
			}
		}
	}

	var lastSync *string
	if last.Valid {
		iso := last.Time.UTC().Format(time.RFC3339)
		lastSync = &iso
	}

	writeJSON(w, http.StatusOK, systemHealthDTO{
		Scheduler:  schedulerDTO{Status: sStatus, LastTickAt: lastTick},
		QueueDepth: qd,
		Workers:    workers,
		LastSyncAt: lastSync,
	})
}
