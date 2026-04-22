package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/audit"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// DefaultAutoPauseAfter is the global default threshold for consecutive
// scheduled-run failures before a schedule is auto-paused. Per-schedule
// overrides from cronfoundry.yaml take precedence.
const DefaultAutoPauseAfter int32 = 5

// evaluateAutoPause decides whether the just-finalized run should trigger
// auto-pause on its schedule. It is called from the run-finalize handler
// AFTER the finalize transaction commits; it opens its own short-lived
// transaction so an evaluation error cannot roll back the load-bearing run
// row. All errors are returned; the caller logs and swallows them.
//
// No-ops unless (fireReason == "schedule" AND runStatus == "failed").
func evaluateAutoPause(
	ctx context.Context,
	pool *pgxpool.Pool,
	scheduleID, runID uuid.UUID,
	runStatus, fireReason string,
) error {
	if fireReason != "schedule" || runStatus != "failed" {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbgen.New(tx)
	schedPG := pgtype.UUID{Bytes: scheduleID, Valid: true}

	cfg, err := q.GetScheduleAutoPauseConfig(ctx, schedPG)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // schedule was deleted; nothing to do
		}
		return fmt.Errorf("evaluateAutoPause: get config: %w", err)
	}

	threshold := DefaultAutoPauseAfter
	if cfg.AutoPauseAfter != nil && *cfg.AutoPauseAfter >= 1 {
		threshold = *cfg.AutoPauseAfter
	}

	statuses, err := q.ListRecentTerminalScheduledRuns(ctx, dbgen.ListRecentTerminalScheduledRunsParams{
		ScheduleID: schedPG,
		CreatedAt:  cfg.LastEnabledAt, // pgtype.Timestamptz; passed as-is
		Limit:      threshold,
	})
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: list recent: %w", err)
	}
	if int32(len(statuses)) < threshold {
		return nil
	}
	for _, s := range statuses {
		if s != "failed" {
			return nil // streak broken
		}
	}

	reason := fmt.Sprintf("%d consecutive failed runs", threshold)
	affected, err := q.AutoPauseSchedule(ctx, dbgen.AutoPauseScheduleParams{
		ID:              schedPG,
		AutoPauseReason: &reason,
	})
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: pause update: %w", err)
	}
	// Under race: if another finalize already paused the schedule, our UPDATE
	// matched 0 rows (WHERE enabled = true). Do NOT emit audit or run_event —
	// those would be duplicates of the winner's writes.
	if affected == 0 {
		return nil
	}

	scheduleUUID := scheduleID
	if err := audit.Log(ctx, q, audit.Entry{
		OrgID:      cfg.OrgID,
		Actor:      "system",
		Action:     "schedule.auto_paused",
		TargetKind: "schedule",
		TargetID:   &scheduleUUID,
		Detail: map[string]any{
			"threshold":   threshold,
			"last_run_id": runID.String(),
		},
	}); err != nil {
		return fmt.Errorf("evaluateAutoPause: audit: %w", err)
	}

	payload, err := json.Marshal(map[string]any{"threshold": threshold})
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: marshal payload: %w", err)
	}
	if err := q.InsertRunEvent(ctx, dbgen.InsertRunEventParams{
		RunID:       pgtype.UUID{Bytes: runID, Valid: true},
		Level:       "info",
		EventType:   "schedule.auto_paused",
		PayloadJson: payload,
	}); err != nil {
		return fmt.Errorf("evaluateAutoPause: run_event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("evaluateAutoPause: commit: %w", err)
	}

	slog.Info("auto-pause triggered",
		"schedule_id", scheduleID,
		"threshold", threshold,
		"run_id", runID)
	return nil
}
