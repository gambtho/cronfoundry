package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestSchedulesHandler_ListEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/schedules", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var rows []any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
	require.Empty(t, rows)
}

func TestSchedules_Pause_AuditLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	var orgID, repoID, skillID, schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM organization LIMIT 1`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'sk', 'sk', 'sha', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, timeout_sec, provider, model, destinations_json)
		 VALUES ($1, $2, 's', '* * * * *', 60, 'openai', 'm', '[]'::jsonb)
		 RETURNING id`, orgID, skillID).Scan(&schedID))

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	schedUUID, err := schedUUIDFromPg(schedID)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/schedules/"+schedUUID+"/pause", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'schedule.pause' AND org_id = $1`,
		orgID).Scan(&count))
	require.Equal(t, 1, count)
}

// schedUUIDFromPg returns the canonical uuid string for a pgtype.UUID.
func schedUUIDFromPg(id pgtype.UUID) (string, error) {
	b, err := id.Value()
	if err != nil {
		return "", err
	}
	return b.(string), nil
}

func TestSchedulesHandler_PauseRequiresAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("POST", "/api/schedules/00000000-0000-0000-0000-000000000000/pause", nil)
	addTestSession(t, req, masterKey, "bob", "viewer")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
}

// seedAutoPausedSchedule inserts a minimal org (if not already present), repo,
// skill, and a schedule that is currently auto-paused (enabled=false,
// auto_paused_at set, auto_pause_reason set). Returns the schedule UUID.
func seedAutoPausedSchedule(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	ctx := context.Background()

	// Ensure an org exists (seedOrg may have already been called, but we need
	// the org's ID here).
	_, _ = pool.Exec(ctx, `INSERT INTO organization (name) VALUES ('test-org') ON CONFLICT DO NOTHING`)

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM organization LIMIT 1`).Scan(&orgID))

	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))

	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'sk', 'sk', 'sha', '{}'::jsonb) RETURNING id`, orgID, repoID).Scan(&skillID))

	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, timeout_sec, provider, model, destinations_json,
		                       enabled, auto_paused_at, auto_pause_reason)
		 VALUES ($1, $2, 's', '* * * * *', 60, 'openai', 'm', '[]'::jsonb,
		         false, now(), 'test')
		 RETURNING id`, orgID, skillID).Scan(&schedID))

	return schedID
}

func TestSchedules_List_JSONKeysAreSnakeCase(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	var orgID, repoID, skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM organization LIMIT 1`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'acme', 'widgets', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha', '{}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	_, err := pool.Exec(ctx, `
		INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json,
		                     enabled, auto_paused_at, auto_pause_reason, auto_pause_after)
		VALUES ($1, $2, 'daily', '0 9 * * *', 'openai', 'gpt-4o', '[]'::jsonb,
		        false, now(), '5 consecutive failed runs', 3)
	`, orgID, skillID)
	require.NoError(t, err)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))
	req := httptest.NewRequest("GET", "/api/schedules", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var rows []map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
	require.Len(t, rows, 1)
	r := rows[0]

	// snake_case keys the frontend depends on
	for _, k := range []string{
		"id", "name", "cron", "enabled", "auto_pause_after",
		"auto_paused_at", "auto_pause_reason", "last_enabled_at",
		"skill_path", "owner", "repo_name",
	} {
		_, ok := r[k]
		assert.Truef(t, ok, "missing key %q in response; got: %v", k, r)
	}
	// PascalCase keys should NOT be present (DTO enforces snake)
	for _, k := range []string{"ID", "Name", "AutoPausedAt"} {
		_, ok := r[k]
		assert.Falsef(t, ok, "unexpected PascalCase key %q in response", k)
	}
	assert.Equal(t, "daily", r["name"])
	assert.Equal(t, false, r["enabled"])
	assert.NotNil(t, r["auto_paused_at"])
	assert.Equal(t, "5 consecutive failed runs", r["auto_pause_reason"])
	assert.InDelta(t, 3.0, r["auto_pause_after"], 0.001) // JSON numbers decode as float64
}

func TestResume_ClearsAutoPauseAndBumpsLastEnabledAt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	schedID := seedAutoPausedSchedule(t, pool)

	// Capture last_enabled_at before resume.
	var beforeLEA pgtype.Timestamptz
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT last_enabled_at FROM schedule WHERE id = $1`, schedID).Scan(&beforeLEA))

	// Simulate a small tick of wall-clock so "bumped" is observable.
	time.Sleep(10 * time.Millisecond)

	// Build the mux and perform resume via the real handler.
	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	schedUUID, err := schedUUIDFromPg(schedID)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/schedules/"+schedUUID+"/resume", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Verify: enabled true, reason/at cleared, last_enabled_at bumped.
	var enabled bool
	var pausedAt pgtype.Timestamptz
	var reason *string
	var afterLEA pgtype.Timestamptz
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at, auto_pause_reason, last_enabled_at FROM schedule WHERE id = $1`,
		schedID).Scan(&enabled, &pausedAt, &reason, &afterLEA))
	assert.True(t, enabled)
	assert.False(t, pausedAt.Valid, "auto_paused_at should be NULL after resume")
	assert.Nil(t, reason)
	assert.True(t, afterLEA.Valid, "last_enabled_at should be set after resume")
	if beforeLEA.Valid {
		assert.True(t, afterLEA.Time.After(beforeLEA.Time), "last_enabled_at should advance on resume")
	}
}
