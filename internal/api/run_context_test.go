package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

func bootPG(t *testing.T) (*pgxpool.Pool, func()) {
	return testdb.BootPG(t)
}

// seedRun creates a full chain (organization → repo_connection → skill →
// schedule → run) and returns the run's UUID (as uuid.UUID) plus org UUID.
// fire_reason='manual' so the CHECK constraint (fire_time IS NULL) is
// satisfied.
func seedRun(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, pgtype.UUID) {
	t.Helper()
	return seedRunWithHash(t, pool, "hash-placeholder")
}

// seedRunWithHash is the same as seedRun but lets the caller pin
// runner_token_hash to the sha-256 of a real bearer token. Needed by tests
// that exercise the bearer middleware (which now cross-checks the hash
// against the run row when a pool is present).
func seedRunWithHash(t *testing.T, pool *pgxpool.Pool, hash string) (uuid.UUID, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	var orgPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgPG))
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 42, 'acme', 'widgets', 'main') RETURNING id`, orgPG).Scan(&repoID))
	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{"name":"a"}'::jsonb) RETURNING id`,
		orgPG, repoID).Scan(&skillID))
	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 's', '* * * * *', 'openai', 'gpt-4o-mini', '[]'::jsonb) RETURNING id`,
		orgPG, skillID).Scan(&schedID))

	var runPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-1', 'pending', 'manual', $3) RETURNING id`,
		orgPG, schedID, hash).Scan(&runPG))

	return uuid.UUID(runPG.Bytes), orgPG
}

// bindRunHash updates run.runner_token_hash to the given value. Use this
// after signing a JWT so the bearer middleware's hash check accepts the
// token — the middleware compares signer.HashToken(bearer) to this column.
func bindRunHash(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID, hash string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE run SET runner_token_hash = $1 WHERE id = $2`,
		hash, pgtype.UUID{Bytes: runID, Valid: true})
	require.NoError(t, err)
}

// seedRunInOrg adds a second run (plus its own repo + skill + schedule)
// under an existing org. Useful for tests that need two distinct runs
// sharing an org (e.g. cross-run scope-check assertions).
func seedRunInOrg(t *testing.T, pool *pgxpool.Pool, orgPG pgtype.UUID) (uuid.UUID, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 42, 'acme', 'other-'||$2, 'main') RETURNING id`, orgPG, suffix).Scan(&repoID))
	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/'||$3, 'x', 'sha-x', '{}'::jsonb) RETURNING id`,
		orgPG, repoID, suffix).Scan(&skillID))
	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 's-'||$3, '* * * * *', 'openai', 'gpt-4o-mini', '[]'::jsonb) RETURNING id`,
		orgPG, skillID, suffix).Scan(&schedID))
	var runPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-x', 'pending', 'manual', 'hash-placeholder') RETURNING id`,
		orgPG, schedID).Scan(&runPG))
	return uuid.UUID(runPG.Bytes), repoID
}

func TestRunContext_ReturnsContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
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

	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, runID.String(), body["run_id"])
	assert.Equal(t, "openai", body["provider"])
	assert.Equal(t, "gpt-4o-mini", body["model"])
	assert.Equal(t, "skills/a", body["skill_path"])
	assert.Equal(t, "sha-1", body["skill_sha"])
	assert.Equal(t, "acme/widgets", body["repo"])
	assert.Equal(t, "main", body["default_branch"])
	assert.EqualValues(t, 42, body["installation_id"])
}

func TestRunContext_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	// Seed two runs under the same org: we'll sign for runA but request
	// runB's URL so the middleware's hash check passes (runA exists + its
	// hash column matches the signed token) and the handler's URL-vs-claim
	// guard is what fires.
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

	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/"+runB.String()+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestRunContext_MissingRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	signer := token.New(randomMaster(t))
	missingRun := uuid.New()
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     missingRun,
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/"+missingRun.String()+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// The bearer middleware's hash check rejects tokens pointing at
	// nonexistent runs before the handler ever runs. Previously the
	// handler returned 404; now the middleware returns 401 which is
	// strictly tighter.
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRunContext_BadURLID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	// Seed a real run so the bearer middleware's hash check passes; then
	// request a non-UUID URL so the handler's URL parse fails with 400.
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

	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/not-a-uuid/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
