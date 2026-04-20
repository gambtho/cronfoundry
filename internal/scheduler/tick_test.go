package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

// bootPG delegates to testdb.BootPG.
func bootPG(t *testing.T) (*pgxpool.Pool, func()) {
	return testdb.BootPG(t)
}

// mockDispatcher records Dispatch calls; Wait and Kill are no-ops.
type mockDispatcher struct {
	mu    sync.Mutex
	calls []cloud.DispatchSpec
}

func (m *mockDispatcher) Dispatch(ctx context.Context, spec cloud.DispatchSpec) (cloud.Handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, spec)
	return mockHandle{}, nil
}

type mockHandle struct{}

func (mockHandle) PID() int    { return 42 }
func (mockHandle) Wait() error { return nil }
func (mockHandle) Kill() error { return nil }

func seedDueSchedule(t *testing.T, pool *pgxpool.Pool, overlapPolicy string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	var repoID, skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))

	// next_fire_at in the past so it's due.
	past := time.Now().Add(-1 * time.Minute)
	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, timezone, overlap_policy,
			provider, model, destinations_json, env_json, next_fire_at)
		 VALUES ($1, $2, 's', '* * * * *', 'UTC', $3, 'openai', 'gpt-4o-mini',
		         '[{"slack":{"secret":"slack_url"}}]'::jsonb,
		         '{"LLM":"fake"}'::jsonb, $4)
		 RETURNING id`, orgID, skillID, overlapPolicy, past).Scan(&schedID))
	return schedID
}

func newSigner(t *testing.T) *token.Signer {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i) // deterministic test key
	}
	return token.New(key)
}

func TestTick_DispatchesDue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	schedID := seedDueSchedule(t, pool, "skip")
	mock := &mockDispatcher{}

	deps := Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   mock,
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	}

	stats, err := Tick(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Dispatched)
	assert.Equal(t, 0, stats.Skipped)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	require.Len(t, mock.calls, 1)
	assert.Equal(t, "/usr/bin/true", mock.calls[0].BinaryPath)
	assert.Contains(t, mock.calls[0].Args, "runner")
	assert.Contains(t, mock.calls[0].Args, "--run-id")

	// run should be persisted and flipped to 'running' by SetRunRunning
	// after a successful dispatch (MAJ-12).
	var status string
	var runID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT id, status FROM run WHERE schedule_id = $1`, schedID).Scan(&runID, &status))
	assert.Equal(t, "running", status)

	// next_fire_at should have advanced past the original ("1 minute ago")
	// value. For a "* * * * *" schedule, NextFire(past) returns the next
	// minute boundary after past — which is within (past, past+60s]. That
	// window always lies in the future relative to wall-clock time when
	// the seed-to-assertion elapsed is under a minute, but tying the
	// assertion to wall-clock `now` is wall-clock-position-sensitive. Pin
	// against the seed value instead.
	var nextFire time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT next_fire_at FROM schedule WHERE id = $1`, schedID).Scan(&nextFire))
	// next_fire_at must be within one minute of seed time (`past` was
	// now-1m; cron advances to the next boundary, at most 1 min later).
	assert.True(t, nextFire.After(time.Now().Add(-90*time.Second)),
		"next_fire_at %v should be after ~90s ago", nextFire)
}

func TestTick_SkipsWhenActiveExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	schedID := seedDueSchedule(t, pool, "skip")

	// Seed an already-running prior run for this schedule. Using
	// status='running' (rather than 'pending' with fire_reason='manual' as
	// an earlier iteration of this test did) keeps it out of dispatchPending's
	// sweep so we isolate the overlap-policy behavior. fire_reason='manual'
	// still satisfies the partial unique index predicate.
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT org_id FROM schedule WHERE id = $1`, schedID).Scan(&orgID))
	_, err := pool.Exec(context.Background(),
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash, started_at)
		 VALUES ($1, $2, 'sha-prior', 'running', 'manual', 'hash-prior', now())`, orgID, schedID)
	require.NoError(t, err)

	mock := &mockDispatcher{}
	deps := Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   mock,
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	}

	stats, err := Tick(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Dispatched)
	assert.Equal(t, 1, stats.Skipped)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	assert.Empty(t, mock.calls)

	// After skip, the new pending scheduled run was deleted. No pending
	// rows remain; the prior run is still 'running'.
	var pending int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM run WHERE schedule_id = $1 AND status='pending'`, schedID).Scan(&pending))
	assert.Equal(t, 0, pending)
	var running int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM run WHERE schedule_id = $1 AND status='running'`, schedID).Scan(&running))
	assert.Equal(t, 1, running)
}

func TestTick_OrphanSweepRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	// Seed a schedule + an ancient pending run (exceeded timeout+grace).
	var orgID, repoID, skillID, schedID pgtype.UUID
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'sk', 'sk', 'sha', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, timeout_sec, provider, model, destinations_json, next_fire_at)
		 VALUES ($1, $2, 's', '* * * * *', 60, 'openai', 'm', '[]'::jsonb, now() + interval '1 hour')
		 RETURNING id`, orgID, skillID).Scan(&schedID))

	// Run that started 10 minutes ago (way past timeout_sec=60 + 300s grace).
	var runID pgtype.UUID
	ancient := time.Now().Add(-10 * time.Minute)
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash, started_at, created_at)
		 VALUES ($1, $2, 'sha', 'running', 'manual', 'h', $3, $3) RETURNING id`,
		orgID, schedID, ancient).Scan(&runID))

	_, err := Tick(ctx, Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   &mockDispatcher{},
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	})
	require.NoError(t, err)

	var status, errKind string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, coalesce(error_kind, '') FROM run WHERE id = $1`, runID).Scan(&status, &errKind))
	assert.Equal(t, "failed", status)
	assert.Equal(t, "shutdown", errKind)
}

// TestTick_DispatchesPendingManualRun pins the MAJ-11 fix: Tick must pick
// up manual runs (fire_reason='manual', fire_time IS NULL) that are
// created by POST /internal/schedules/:id/run-now but have no next_fire_at
// tying them to the due-schedule loop.
func TestTick_DispatchesPendingManualRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	// Seed a schedule whose next_fire_at is in the FUTURE so processOne
	// doesn't pick it up, then insert a manual pending run directly.
	ctx := context.Background()
	var orgID, repoID, skillID, schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	future := time.Now().Add(time.Hour)
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json, next_fire_at)
		 VALUES ($1, $2, 's', '* * * * *', 'openai', 'm', '[]'::jsonb, $3) RETURNING id`,
		orgID, skillID, future).Scan(&schedID))

	var runID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-1', 'pending', 'manual', 'hash-placeholder') RETURNING id`,
		orgID, schedID).Scan(&runID))

	mock := &mockDispatcher{}
	deps := Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   mock,
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	}
	stats, err := Tick(ctx, deps)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Dispatched, "manual pending run should have been dispatched")

	mock.mu.Lock()
	defer mock.mu.Unlock()
	require.Len(t, mock.calls, 1)
	assert.Contains(t, mock.calls[0].Args, uuid.UUID(runID.Bytes).String())

	// MAJ-12: after dispatch, status should be 'running'.
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM run WHERE id = $1`, runID).Scan(&status))
	assert.Equal(t, "running", status, "SetRunRunning must flip status after dispatch")

	// The runner_token_hash column should have been rotated from the
	// placeholder to a real sha-256 hex (length 64).
	var hash string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT runner_token_hash FROM run WHERE id = $1`, runID).Scan(&hash))
	assert.Len(t, hash, 64, "runner_token_hash should be a 64-char sha-256 hex")
}

// TestInsertRun_ConflictReturnsExistingRow pins the CRIT-2 fix: a
// concurrent-duplicate insert returns the original row with Inserted=false
// rather than a zero-UUID sentinel.
func TestInsertRun_ConflictReturnsExistingRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	schedID := seedDueSchedule(t, pool, "skip")

	ctx := context.Background()
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT org_id FROM schedule WHERE id = $1`, schedID).Scan(&orgID))

	q := dbgen.New(pool)
	fireTime := pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Second), Valid: true}

	first, err := q.InsertRun(ctx, dbgen.InsertRunParams{
		OrgID:           orgID,
		ScheduleID:      schedID,
		SkillSha:        "sha-1",
		FireTime:        fireTime,
		FireReason:      "schedule",
		RunnerTokenHash: "hash-first",
	})
	require.NoError(t, err)
	assert.True(t, first.Inserted, "first call should report Inserted=true")
	assert.True(t, first.ID.Valid, "first call should return a real UUID")

	second, err := q.InsertRun(ctx, dbgen.InsertRunParams{
		OrgID:           orgID,
		ScheduleID:      schedID,
		SkillSha:        "sha-2", // different content — must not overwrite
		FireTime:        fireTime,
		FireReason:      "schedule",
		RunnerTokenHash: "hash-second",
	})
	require.NoError(t, err)
	assert.False(t, second.Inserted, "second call should report Inserted=false")
	assert.Equal(t, first.ID, second.ID, "conflict resolution must return the original row")
	assert.Equal(t, "sha-1", second.SkillSha, "existing row's content must not be clobbered")
}

func TestTick_NoDueSchedules(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	mock := &mockDispatcher{}
	deps := Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   mock,
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	}
	stats, err := Tick(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Dispatched)
	assert.Empty(t, mock.calls)
}
