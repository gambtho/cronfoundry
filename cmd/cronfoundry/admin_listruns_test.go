package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminListRuns_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	var buf bytes.Buffer
	require.NoError(t, runAdminListRuns(context.Background(), 10, "", &buf))
	assert.Contains(t, buf.String(), "no runs")
}

func TestAdminListRuns_ShowsRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	seedScheduleWithFinishedRun(t, dsn)

	var buf bytes.Buffer
	require.NoError(t, runAdminListRuns(context.Background(), 10, "", &buf))
	out := buf.String()
	assert.Contains(t, out, "RUN ID")
	assert.Contains(t, out, "succeeded")
}

func seedScheduleWithFinishedRun(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	var orgID, repoID, skillID, schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM organization LIMIT 1`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'sk', 's', 'sha', '{}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 'monday', '0 9 * * MON', 'openai', 'gpt-4o-mini', '[]'::jsonb)
		 RETURNING id`, orgID, skillID).Scan(&schedID))
	_, err = pool.Exec(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
		                  runner_token_hash, started_at, finished_at, duration_ms,
		                  tokens_in, tokens_out)
		 VALUES ($1, $2, 'sha', 'succeeded', 'manual', 'h',
		         now() - interval '10 seconds', now(), 10000, 500, 100)`,
		orgID, schedID)
	require.NoError(t, err)
}
