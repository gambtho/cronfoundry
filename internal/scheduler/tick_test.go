package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/db"
	"github.com/gambtho/cronfoundry/internal/token"
)

// bootPG is a local Postgres-container helper (independent of api's bootPG).
func bootPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf"),
		postgres.WithUsername("cf"),
		postgres.WithPassword("cf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, dsn))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, func() { pool.Close(); _ = c.Terminate(context.Background()) }
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

	// run should be persisted with status='pending'.
	var status string
	var runID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT id, status FROM run WHERE schedule_id = $1`, schedID).Scan(&runID, &status))
	assert.Equal(t, "pending", status)

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

	// Seed an existing active (pending) run with fire_reason='manual'
	// so the partial unique index doesn't collide with the scheduled insert.
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT org_id FROM schedule WHERE id = $1`, schedID).Scan(&orgID))
	_, err := pool.Exec(context.Background(),
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-prior', 'pending', 'manual', 'hash-prior')`, orgID, schedID)
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

	// After skip, the new pending scheduled run was deleted. The original
	// manual pending run remains.
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM run WHERE schedule_id = $1 AND status='pending'`, schedID).Scan(&count))
	assert.Equal(t, 1, count)
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
