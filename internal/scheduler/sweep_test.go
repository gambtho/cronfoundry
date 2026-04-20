package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
)

func TestSweepOrphans_ReclaimsStalledRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	ctx := context.Background()
	var orgID, repoID, skillID, schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
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

	// Ancient running row.
	ancient := time.Now().Add(-10 * time.Minute)
	var runID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash, started_at, created_at)
		 VALUES ($1, $2, 'sha', 'running', 'manual', 'h', $3, $3) RETURNING id`,
		orgID, schedID, ancient).Scan(&runID))

	n, err := SweepOrphans(ctx, Deps{Pool: pool})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	var status, errKind string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, coalesce(error_kind, '') FROM run WHERE id = $1`, runID).Scan(&status, &errKind))
	assert.Equal(t, "failed", status)
	assert.Equal(t, "shutdown", errKind)
}

func TestSweepOrphans_NoOpOnFreshRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	n, err := SweepOrphans(context.Background(), Deps{Pool: pool})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
}
