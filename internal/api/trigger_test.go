package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedSchedule creates an org -> repo -> skill -> schedule chain (no run).
// Returns the schedule ID and the org ID.
func seedSchedule(t *testing.T, pool *pgxpool.Pool) (schedID, orgID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	var repoID, skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 's', '* * * * *', 'openai', 'gpt-4o-mini', '[]'::jsonb) RETURNING id`,
		orgID, skillID).Scan(&schedID))
	return schedID, orgID
}

func TestRunNow_CreatesPendingRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	schedID, _ := seedSchedule(t, pool)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]string{"actor": "alice"}
	buf, _ := json.Marshal(body)
	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/"+uuidString(schedID)+"/run-now",
		"application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.NotEmpty(t, out.RunID)

	// Verify a pending run row with fire_reason='manual' and NULL fire_time.
	var status, fireReason string
	var fireTime *string
	var actor *string
	var skillSha string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, fire_reason, fire_time::text, actor, skill_sha FROM run WHERE schedule_id = $1`,
		schedID).Scan(&status, &fireReason, &fireTime, &actor, &skillSha))
	assert.Equal(t, "pending", status)
	assert.Equal(t, "manual", fireReason)
	assert.Nil(t, fireTime, "fire_time must be NULL for manual fires")
	require.NotNil(t, actor)
	assert.Equal(t, "alice", *actor)
	assert.Equal(t, "sha-1", skillSha)
}

func TestRunNow_MissingSchedule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Valid-format UUID that doesn't exist.
	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/00000000-0000-0000-0000-000000000000/run-now",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRunNow_BadScheduleID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/not-a-uuid/run-now",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRunNow_NoActor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	schedID, _ := seedSchedule(t, pool)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Empty body: actor omitted.
	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/"+uuidString(schedID)+"/run-now",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var actor *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT actor FROM run WHERE schedule_id = $1`, schedID).Scan(&actor))
	assert.Nil(t, actor)
}

// uuidString converts a pgtype.UUID back to its canonical string form.
func uuidString(u pgtype.UUID) string {
	b, _ := u.Value() // returns (driver.Value, error); here the Value is a string
	if s, ok := b.(string); ok {
		return s
	}
	return ""
}
