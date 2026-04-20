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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/db"
	"github.com/gambtho/cronfoundry/internal/token"
)

// bootPG starts a throwaway Postgres container, runs migrations, and returns
// a ready-to-use pool.
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

// seedRun creates a full chain (organization → repo_connection → skill →
// schedule → run) and returns the run's UUID (as uuid.UUID) plus org UUID.
// fire_reason='manual' so the CHECK constraint (fire_time IS NULL) is
// satisfied.
func seedRun(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, pgtype.UUID) {
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
		 VALUES ($1, $2, 'sha-1', 'pending', 'manual', 'hash-placeholder') RETURNING id`,
		orgPG, schedID).Scan(&runPG))

	return uuid.UUID(runPG.Bytes), orgPG
}

func TestRunContext_ReturnsContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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

	actualRun, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	// Sign a token for a DIFFERENT run_id.
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/"+actualRun.String()+"/context", nil)
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
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRunContext_BadURLID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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
