package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestFinalize_PersistsSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{
		"status":               "succeeded",
		"duration_ms":          1234,
		"tokens_in":            400,
		"tokens_out":           120,
		"cost_cents":           1,
		"writeback_commit_sha": "abc1234",
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify DB state.
	var status string
	var durationMs *int32
	var tokensIn *int32
	var wbSha *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, duration_ms, tokens_in, writeback_commit_sha FROM run WHERE id = $1`,
		pgtype.UUID{Bytes: runID, Valid: true}).Scan(&status, &durationMs, &tokensIn, &wbSha))
	assert.Equal(t, "succeeded", status)
	require.NotNil(t, durationMs)
	assert.EqualValues(t, 1234, *durationMs)
	require.NotNil(t, tokensIn)
	assert.EqualValues(t, 400, *tokensIn)
	require.NotNil(t, wbSha)
	assert.Equal(t, "abc1234", *wbSha)

	// finished_at should be set.
	var finishedAt *time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT finished_at FROM run WHERE id = $1`, pgtype.UUID{Bytes: runID, Valid: true}).Scan(&finishedAt))
	assert.NotNil(t, finishedAt)
}

func TestFinalize_PersistsFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{
		"status":     "failed",
		"error_kind": "llm",
		"error_msg":  "429 rate limit",
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var status string
	var errKind, errMsg *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, error_kind, error_msg FROM run WHERE id = $1`,
		pgtype.UUID{Bytes: runID, Valid: true}).Scan(&status, &errKind, &errMsg))
	assert.Equal(t, "failed", status)
	require.NotNil(t, errKind)
	assert.Equal(t, "llm", *errKind)
	require.NotNil(t, errMsg)
	assert.Equal(t, "429 rate limit", *errMsg)
}

func TestFinalize_RejectsInvalidStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	buf, _ := json.Marshal(map[string]any{"status": "weird"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestFinalize_RejectsNegativeAccounting pins the MAJ-6 fix: runners must
// not be able to log negative token counts, durations, or costs.
func TestFinalize_RejectsNegativeAccounting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	fields := []string{"duration_ms", "tokens_in", "tokens_out", "cost_cents"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			body := map[string]any{"status": "succeeded", field: -1}
			buf, _ := json.Marshal(body)
			req, _ := http.NewRequest("POST",
				ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := ts.Client().Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
				"negative %s must be rejected", field)
		})
	}
}

// TestFinalize_RejectsAlreadyTerminal pins the MAJ-7 fix: a crashed-then-
// restarted runner can't clobber the original terminal state.
func TestFinalize_RejectsAlreadyTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	// Force the run into a terminal state directly.
	_, err := pool.Exec(context.Background(),
		`UPDATE run SET status='succeeded', finished_at=now() WHERE id=$1`,
		pgtype.UUID{Bytes: runID, Valid: true})
	require.NoError(t, err)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{"status": "failed", "error_msg": "restart"}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST",
		ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// The original status must be unchanged.
	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM run WHERE id = $1`,
		pgtype.UUID{Bytes: runID, Valid: true}).Scan(&status))
	assert.Equal(t, "succeeded", status)
}

func TestFinalize_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Sign for runA (seeded, hash bound); POST to runB's finalize URL so
	// the middleware accepts the token and the handler's URL-vs-claim
	// guard fires with 403.
	runA, orgID := seedRun(t, pool)
	runB, _ := seedRunInOrg(t, pool, orgID)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runA,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runA, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	buf, _ := json.Marshal(map[string]any{"status": "succeeded"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runB.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestFinalize_TriggersAutoPauseAfterConsecutiveFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Seed a schedule with 4 prior failed scheduled runs (inline like the
	// existing seedRunWithHash helper — this package does not expose
	// smaller seedOrg/seedRepo helpers).
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
		INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		VALUES ($1, $2, 'daily', '0 9 * * *', 'openai', 'gpt-4o', '[]'::jsonb)
		RETURNING id
	`, orgID, skillID).Scan(&scheduleID))

	// Backdate last_enabled_at so the seeded runs sit in the past rather than
	// the future, even though the evaluator doesn't care about wall-clock
	// "now" — this keeps test fixtures legible.
	_, err := pool.Exec(ctx,
		`UPDATE schedule SET last_enabled_at = now() - interval '1 hour' WHERE id = $1`,
		scheduleID)
	require.NoError(t, err)

	var enabledAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT last_enabled_at FROM schedule WHERE id=$1`, scheduleID).Scan(&enabledAt))

	base := enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		_, err := pool.Exec(ctx, `
			INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
			                 fire_time, started_at, finished_at, runner_token_hash, created_at)
			VALUES ($1, $2, 'abc', 'failed', 'schedule', $3, $3, $3, 'hash-seed', $3)
		`, orgID, scheduleID, ts)
		require.NoError(t, err)
	}

	// Create a 5th pending run under the same org/schedule and finalize it via the handler.
	fiveFireTime := base.Add(5 * time.Minute)
	var runPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, fire_time, runner_token_hash, created_at)
		VALUES ($1, $2, 'sha-1', 'pending', 'schedule', $3, 'placeholder', $3)
		RETURNING id
	`, orgID, scheduleID, fiveFireTime).Scan(&runPG))
	runID := uuid.UUID(runPG.Bytes)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	errKind := "llm_error"
	body := map[string]any{"status": "failed", "error_kind": errKind}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Assert pause landed.
	var enabled bool
	var pausedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT enabled, auto_paused_at FROM schedule WHERE id=$1`, scheduleID).Scan(&enabled, &pausedAt))
	assert.False(t, enabled, "schedule must be disabled by auto-pause")
	require.NotNil(t, pausedAt, "auto_paused_at must be stamped")
}
