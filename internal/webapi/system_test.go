package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/scheduler"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func systemDoGet(t *testing.T, mux *http.ServeMux, masterKey []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/system/health", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var body map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	return rr, body
}

func TestSystemHealth_NilClock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	deps := testDeps(pool, masterKey)
	deps.SweepInterval = time.Second
	webapi.RegisterRoutes(mux, deps)

	_, body := systemDoGet(t, mux, masterKey)
	sched := body["scheduler"].(map[string]any)
	assert.Equal(t, "down", sched["status"])
	assert.Nil(t, sched["last_tick_at"])
	assert.EqualValues(t, 0, body["queue_depth"])
	assert.EqualValues(t, 0, body["workers"])
	assert.Nil(t, body["last_sync_at"])
}

func TestSystemHealth_SchedulerStatusBranches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	cases := []struct {
		name   string
		ago    time.Duration
		status string
	}{
		{"healthy", 500 * time.Millisecond, "healthy"},
		{"degraded", 3 * time.Second, "degraded"},
		{"down", 6 * time.Second, "down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool, cleanup := testdb.BootPG(t)
			defer cleanup()
			seedOrg(t, pool)

			masterKey := make([]byte, 32)
			clk := &scheduler.TickClock{}
			clk.Record(time.Now().Add(-tc.ago))

			deps := testDeps(pool, masterKey)
			deps.Clock = clk
			deps.SweepInterval = time.Second

			mux := http.NewServeMux()
			webapi.RegisterRoutes(mux, deps)

			_, body := systemDoGet(t, mux, masterKey)
			sched := body["scheduler"].(map[string]any)
			assert.Equal(t, tc.status, sched["status"])
			assert.NotNil(t, sched["last_tick_at"])
		})
	}
}

func TestSystemHealth_QueueAndWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	var orgID, repoID, skillID, schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM organization LIMIT 1`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 's', 's', 'sha', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, timeout_sec, provider, model, destinations_json)
		 VALUES ($1, $2, 's', '* * * * *', 60, 'openai', 'm', '[]'::jsonb) RETURNING id`,
		orgID, skillID).Scan(&schedID))

	now := time.Now()
	_, err := pool.Exec(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash, runner_pid, started_at, created_at)
		 VALUES ($1, $2, 'sha', 'running', 'manual', 'h', 12345, $3, $3)`,
		orgID, schedID, now)
	require.NoError(t, err)

	masterKey := make([]byte, 32)
	clk := &scheduler.TickClock{}
	clk.Record(time.Now())

	deps := testDeps(pool, masterKey)
	deps.Clock = clk
	deps.SweepInterval = time.Second

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, deps)

	_, body := systemDoGet(t, mux, masterKey)
	assert.GreaterOrEqual(t, int64(body["queue_depth"].(float64)), int64(1))
	assert.GreaterOrEqual(t, int64(body["workers"].(float64)), int64(1))
	assert.NotNil(t, body["last_sync_at"])
	// Verify it's a string ISO timestamp.
	_, ok := body["last_sync_at"].(string)
	assert.True(t, ok)

	// Verify the Queries layer matches.
	q := dbgen.New(pool)
	qd, err := q.CountQueueDepth(ctx, orgID)
	require.NoError(t, err)
	assert.EqualValues(t, qd, int64(body["queue_depth"].(float64)))
}
