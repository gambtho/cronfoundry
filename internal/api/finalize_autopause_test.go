package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
)

// apHarness bundles a seeded DB + helpers used by every auto-pause case.
type apHarness struct {
	pool       *pgxpool.Pool
	orgID      pgtype.UUID
	scheduleID pgtype.UUID
	skillID    pgtype.UUID
	enabledAt  time.Time
}

func newAPHarness(t *testing.T, autoPauseAfter *int32) apHarness {
	t.Helper()
	pool, cleanup := testdb.BootPG(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 42, 'acme', 'widgets', 'main') RETURNING id`, orgID).Scan(&repoID))
	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{"name":"a"}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))

	var scheduleID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO schedule (org_id, skill_id, name, cron, provider, model,
		                     destinations_json, auto_pause_after)
		VALUES ($1, $2, 'daily', '0 9 * * *', 'openai', 'gpt-4o',
		        '[]'::jsonb, $3)
		RETURNING id
	`, orgID, skillID, autoPauseAfter).Scan(&scheduleID))

	var enabledAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT last_enabled_at FROM schedule WHERE id = $1`, scheduleID).Scan(&enabledAt))

	return apHarness{pool: pool, orgID: orgID, scheduleID: scheduleID, skillID: skillID, enabledAt: enabledAt}
}

// seedRun creates a single run row; used to build failure histories.
// status must be one of succeeded/partial_failure/failed. fireReason is
// "schedule" or "manual". startedAt is explicit so tests can position
// runs relative to last_enabled_at. We also set created_at explicitly
// because the ListRecentTerminalScheduledRuns query filters on created_at.
//
// For schedule runs, fire_time is required by the check constraint
// run_fire_reason_time_consistent; we use startedAt as the fire_time
// (each call uses a distinct time so the unique partial index doesn't conflict).
func (h apHarness) seedRun(t *testing.T, status, fireReason string, startedAt time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	var fireTime *time.Time
	if fireReason == "schedule" {
		fireTime = &startedAt
	}
	require.NoError(t, h.pool.QueryRow(context.Background(), `
		INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
		                 fire_time, started_at, finished_at, created_at, runner_token_hash)
		VALUES ($1, $2, 'abc123', $3, $4, $6, $5, $5, $5, 'hash-for-test')
		RETURNING id
	`, h.orgID, h.scheduleID, status, fireReason, startedAt, fireTime).Scan(&id))
	return id
}

// assertPaused confirms the schedule is enabled=false with stamped auto-pause
// state and that at least one audit_log row with action='schedule.auto_paused'
// exists for the schedule.
func (h apHarness) assertPaused(t *testing.T) {
	t.Helper()
	var enabled bool
	var pausedAt *time.Time
	var reason *string
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at, auto_pause_reason FROM schedule WHERE id = $1`,
		h.scheduleID).Scan(&enabled, &pausedAt, &reason))
	assert.False(t, enabled, "schedule should be disabled")
	require.NotNil(t, pausedAt, "auto_paused_at should be set")
	require.NotNil(t, reason, "auto_pause_reason should be set")
	assert.Contains(t, *reason, "consecutive failed runs")

	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'schedule.auto_paused' AND target_id = $1`,
		h.scheduleID).Scan(&n))
	assert.GreaterOrEqual(t, n, 1, "at least one schedule.auto_paused audit row")

	var nEvents int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM run_event WHERE event_type='schedule.auto_paused'`).Scan(&nEvents))
	assert.GreaterOrEqual(t, nEvents, 1, "at least one schedule.auto_paused run_event row")
}

// assertNotPaused confirms the schedule is still enabled and no auto-pause
// state is set.
func (h apHarness) assertNotPaused(t *testing.T) {
	t.Helper()
	var enabled bool
	var pausedAt *time.Time
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at FROM schedule WHERE id = $1`,
		h.scheduleID).Scan(&enabled, &pausedAt))
	assert.True(t, enabled, "schedule should still be enabled")
	assert.Nil(t, pausedAt, "auto_paused_at should still be null")
}

func TestEvaluateAutoPause_TriggersAtDefaultThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil) // threshold = DefaultAutoPauseAfter (5)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(5*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertPaused(t)
}

func TestEvaluateAutoPause_SuccessBreaksStreak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 3; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	h.seedRun(t, "succeeded", "schedule", base.Add(3*time.Minute))
	last := h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertNotPaused(t)
}

func TestEvaluateAutoPause_PartialFailureBreaksStreak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 3; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	h.seedRun(t, "partial_failure", "schedule", base.Add(3*time.Minute))
	last := h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertNotPaused(t)
}

func TestEvaluateAutoPause_ManualFailureExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "manual", base.Add(5*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "manual")
	require.NoError(t, err)

	h.assertNotPaused(t) // manual run doesn't trigger evaluation at all
}

func TestEvaluateAutoPause_AntiFlapWindowExcludesOldRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	// One run just BEFORE the enable window should be excluded — use -1ns
	// to pin the boundary: the query's `created_at >= last_enabled_at`
	// must use `>=`, not `>`, and anything strictly less is excluded.
	h.seedRun(t, "failed", "schedule", h.enabledAt.Add(-time.Nanosecond))
	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 3; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertNotPaused(t) // only 4 in-window runs, not 5
}

func TestEvaluateAutoPause_PerScheduleOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	threshold := int32(3)
	h := newAPHarness(t, &threshold)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 2; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(3*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertPaused(t)
}

func TestEvaluateAutoPause_IdempotentWhenAlreadyPaused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(5*time.Minute))

	// First call pauses.
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule"))
	h.assertPaused(t)

	var countBefore int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action='schedule.auto_paused' AND target_id=$1`,
		h.scheduleID).Scan(&countBefore))

	var eventsBefore int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM run_event WHERE event_type='schedule.auto_paused'`).Scan(&eventsBefore))

	// Second call: schedule is already paused; UPDATE affects 0 rows; no new audit or run_event.
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule"))

	var countAfter int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action='schedule.auto_paused' AND target_id=$1`,
		h.scheduleID).Scan(&countAfter))
	assert.Equal(t, countBefore, countAfter, "no duplicate audit rows on re-pause")

	var eventsAfter int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM run_event WHERE event_type='schedule.auto_paused'`).Scan(&eventsAfter))
	assert.Equal(t, eventsBefore, eventsAfter, "no duplicate run_event rows on re-pause")
}

// TestEvaluateAutoPause_ScheduleIgnoresManualHistory verifies that the SQL
// filter `fire_reason = 'schedule'` in ListRecentTerminalScheduledRuns
// correctly excludes manual runs from the streak count. We seed a history
// where a manual 'succeeded' run sits between scheduled failures: if the
// filter works, the five scheduled failures trigger a pause; if the filter
// is broken the succeeded row would break the streak and suppress the pause.
func TestEvaluateAutoPause_ScheduleIgnoresManualHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil) // threshold = DefaultAutoPauseAfter (5)

	base := h.enabledAt.Add(time.Second)
	h.seedRun(t, "failed", "schedule", base.Add(1*time.Minute))
	// Manual "succeeded" run as noise — must not pollute the streak count.
	h.seedRun(t, "succeeded", "manual", base.Add(2*time.Minute))
	h.seedRun(t, "failed", "schedule", base.Add(3*time.Minute))
	h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))
	h.seedRun(t, "failed", "schedule", base.Add(5*time.Minute))
	last := h.seedRun(t, "failed", "schedule", base.Add(6*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertPaused(t)
}

func TestEvaluateAutoPause_ThresholdOneTriggersAtFirstFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	threshold := int32(1)
	h := newAPHarness(t, &threshold)

	last := h.seedRun(t, "failed", "schedule", h.enabledAt.Add(time.Second))
	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)
	h.assertPaused(t)
}

func TestEvaluateAutoPause_NoOpForNonFailedOrNonScheduled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	// Succeeded run: no-op regardless of history.
	last := h.seedRun(t, "succeeded", "schedule", h.enabledAt.Add(time.Second))
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "succeeded", "schedule"))
	h.assertNotPaused(t)

	// Manual failed run: no-op regardless of history.
	manual := h.seedRun(t, "failed", "manual", h.enabledAt.Add(2*time.Second))
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(manual.Bytes), "failed", "manual"))
	h.assertNotPaused(t)
}
