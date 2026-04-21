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

	"github.com/gambtho/cronfoundry/internal/testdb"
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
	pool, cleanup := testdb.BootPG(t)
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
	defer func() { _ = resp.Body.Close() }()
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
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Valid-format UUID that doesn't exist.
	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/00000000-0000-0000-0000-000000000000/run-now",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRunNow_BadScheduleID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/not-a-uuid/run-now",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRunNow_NoActor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
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
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var actor *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT actor FROM run WHERE schedule_id = $1`, schedID).Scan(&actor))
	assert.Nil(t, actor)
}

// TestRunNow_RejectsNonLoopback pins the MAJ-9 fix: until P3 gates the
// endpoint behind UI session auth, run-now is loopback-only. A request
// with a non-loopback RemoteAddr must be rejected with 403.
func TestRunNow_RejectsNonLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	schedID, _ := seedSchedule(t, pool)

	// Exercise the handler directly via ServeHTTP so we can forge
	// RemoteAddr (httptest.NewServer would route from a real loopback
	// listener, which the check correctly accepts).
	h := runNowHandler{deps: Deps{Pool: pool}}
	// Wrap in a routing mux so the `{id}` path value works.
	mux := http.NewServeMux()
	mux.Handle("POST /internal/schedules/{id}/run-now", h)

	buf := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequest("POST",
		"/internal/schedules/"+uuidString(schedID)+"/run-now", buf)
	req.RemoteAddr = "203.0.113.1:54321" // TEST-NET-3, clearly non-loopback
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// TestTrigger_WritesAuditRow confirms that a successful run-now call emits
// exactly one audit_log row with the expected action/actor/target and a
// detail payload containing the new run_id. This endpoint is the single
// source of truth for schedule.run_now audits (the webapi handler no
// longer writes its own row — it forwards here).
func TestTrigger_WritesAuditRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	schedID, orgID := seedSchedule(t, pool)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]string{"actor": "alice"}
	buf, _ := json.Marshal(body)
	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/"+uuidString(schedID)+"/run-now",
		"application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.RunID)

	// Assert exactly one matching audit row exists with the expected
	// columns and that detail_json carries the new run_id.
	var (
		count      int
		actor      *string
		targetKind *string
		targetID   pgtype.UUID
		detailJSON []byte
	)
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_log
		 WHERE org_id = $1 AND action = 'schedule.run_now'`, orgID).Scan(&count))
	assert.Equal(t, 1, count, "exactly one schedule.run_now audit row expected")

	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT actor, target_kind, target_id, detail_json
		 FROM audit_log
		 WHERE org_id = $1 AND action = 'schedule.run_now'`, orgID).
		Scan(&actor, &targetKind, &targetID, &detailJSON))

	require.NotNil(t, actor)
	assert.Equal(t, "alice", *actor)
	require.NotNil(t, targetKind)
	assert.Equal(t, "schedule", *targetKind)
	assert.Equal(t, uuidString(schedID), uuidString(targetID))

	var detail map[string]any
	require.NoError(t, json.Unmarshal(detailJSON, &detail))
	assert.Equal(t, out.RunID, detail["run_id"])
}

// TestTrigger_AuditFallsBackToSystemActor confirms that when the body
// omits actor (or provides an empty string), the audit row records the
// actor as "system" rather than NULL. Covers the CLI-invoked path where
// a human login is not available.
func TestTrigger_AuditFallsBackToSystemActor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	schedID, orgID := seedSchedule(t, pool)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Empty body: Actor omitted.
	resp, err := ts.Client().Post(
		ts.URL+"/internal/schedules/"+uuidString(schedID)+"/run-now",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var actor *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT actor FROM audit_log
		 WHERE org_id = $1 AND action = 'schedule.run_now'`, orgID).Scan(&actor))
	require.NotNil(t, actor)
	assert.Equal(t, "system", *actor)
}

// uuidString converts a pgtype.UUID back to its canonical string form.
func uuidString(u pgtype.UUID) string {
	b, _ := u.Value() // returns (driver.Value, error); here the Value is a string
	if s, ok := b.(string); ok {
		return s
	}
	return ""
}
