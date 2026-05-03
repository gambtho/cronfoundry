package webapi

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
	"github.com/gambtho/cronfoundry/internal/testdb"
)

func TestAlerts_QuietJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO organization (name) VALUES ('test-org')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO organization (name) VALUES ('other-org')`)
	require.NoError(t, err)

	var orgID, otherID, repoID, skillID, otherSkillID, otherRepoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM organization WHERE name='test-org'`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM organization WHERE name='other-org'`).Scan(&otherID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 2, 'o2', 'r2', 'main') RETURNING id`, otherID).Scan(&otherRepoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 's', 's', 'sha', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 's', 's', 'sha', '{}'::jsonb) RETURNING id`, otherID, otherRepoID).Scan(&otherSkillID))

	fixedNow := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	insertSched := func(orgID, skillID pgtype.UUID, name, cron string, enabled bool, lastSuccess, autoPausedAt time.Time, reason string) pgtype.UUID {
		var schedID pgtype.UUID
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO schedule (org_id, skill_id, name, cron, timezone, timeout_sec, provider, model, destinations_json, enabled)
			 VALUES ($1, $2, $3, $4, 'UTC', 60, 'openai', 'm', '[]'::jsonb, $5) RETURNING id`,
			orgID, skillID, name, cron, enabled).Scan(&schedID))
		if !lastSuccess.IsZero() {
			_, err := pool.Exec(ctx,
				`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash, runner_pid, started_at, finished_at, created_at)
				 VALUES ($1, $2, 'sha', 'succeeded', 'manual', 'h', 12345, $3, $3, $3)`,
				orgID, schedID, lastSuccess)
			require.NoError(t, err)
		}
		if !autoPausedAt.IsZero() {
			_, err := pool.Exec(ctx,
				`UPDATE schedule SET auto_paused_at=$1, auto_pause_reason=$2 WHERE id=$3`,
				autoPausedAt, reason, schedID)
			require.NoError(t, err)
		}
		return schedID
	}

	// fast: every minute, succeeded 5min ago — not quiet (threshold = 1h floor).
	insertSched(orgID, skillID, "fast", "* * * * *", true, fixedNow.Add(-5*time.Minute), time.Time{}, "")
	// slow: hourly, succeeded 4h ago — quiet (threshold = 3h).
	insertSched(orgID, skillID, "slow", "0 * * * *", true, fixedNow.Add(-4*time.Hour), time.Time{}, "")
	// never: every 5 min, never succeeded — quiet.
	insertSched(orgID, skillID, "never", "*/5 * * * *", true, time.Time{}, time.Time{}, "")
	// disabled: should not appear.
	insertSched(orgID, skillID, "disabled", "* * * * *", false, time.Time{}, time.Time{}, "")
	// auto-paused 3d ago — should appear in recently_paused.
	pausedReason := "5 consecutive failures"
	insertSched(orgID, skillID, "paused-recent", "0 * * * *", true, time.Time{}, fixedNow.Add(-3*24*time.Hour), pausedReason)
	// auto-paused 10d ago — must NOT appear.
	insertSched(orgID, skillID, "paused-old", "0 * * * *", true, time.Time{}, fixedNow.Add(-10*24*time.Hour), "old")
	// other-org schedule — must be invisible.
	insertSched(otherID, otherSkillID, "other-org-job", "*/5 * * * *", true, time.Time{}, time.Time{}, "")

	q := dbgen.New(pool)
	h := &alertsHandler{
		deps: Deps{Queries: q},
		now:  func() time.Time { return fixedNow },
	}

	req := httptest.NewRequest("GET", "/api/alerts", nil)
	rr := httptest.NewRecorder()
	h.list(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp alertsDTO
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))

	// quiet_jobs: should contain "slow" and "never", not "fast" or "disabled" or other-org.
	names := make(map[string]quietJobDTO)
	for _, q := range resp.QuietJobs {
		names[q.ScheduleName] = q
	}
	assert.Contains(t, names, "slow")
	assert.Contains(t, names, "never")
	assert.NotContains(t, names, "fast")
	assert.NotContains(t, names, "disabled")
	assert.NotContains(t, names, "other-org-job")

	// "never" has no last_success.
	assert.Nil(t, names["never"].LastSuccess)
	// "slow" has a last_success ISO string.
	require.NotNil(t, names["slow"].LastSuccess)
	assert.NotEmpty(t, *names["slow"].LastSuccess)
	// expected_every for hourly == 3600s.
	assert.Equal(t, int64(3600), names["slow"].ExpectedEvery)
	// expected_every for */5 == 300s.
	assert.Equal(t, int64(300), names["never"].ExpectedEvery)

	// recently_paused: contains "paused-recent" only.
	require.Len(t, resp.RecentlyPaused, 1)
	assert.Equal(t, "paused-recent", resp.RecentlyPaused[0].ScheduleName)
	require.NotNil(t, resp.RecentlyPaused[0].Reason)
	assert.Equal(t, pausedReason, *resp.RecentlyPaused[0].Reason)
	assert.NotEmpty(t, resp.RecentlyPaused[0].PausedAt)

	// reserved arrays must marshal as [] (not null).
	raw := rr.Body.Bytes()
	_ = raw // body already consumed; re-marshal resp instead.
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	bs := string(body)
	assert.Contains(t, bs, `"expiring_secrets":[]`)
	assert.Contains(t, bs, `"drift":[]`)
}

func TestAlerts_EmptyArrays(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO organization (name) VALUES ('test-org')`)
	require.NoError(t, err)

	q := dbgen.New(pool)
	h := &alertsHandler{
		deps: Deps{Queries: q},
		now:  func() time.Time { return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC) },
	}
	req := httptest.NewRequest("GET", "/api/alerts", nil)
	rr := httptest.NewRecorder()
	h.list(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	bs := rr.Body.String()
	assert.Contains(t, bs, `"quiet_jobs":[]`)
	assert.Contains(t, bs, `"recently_paused":[]`)
	assert.Contains(t, bs, `"expiring_secrets":[]`)
	assert.Contains(t, bs, `"drift":[]`)
}
