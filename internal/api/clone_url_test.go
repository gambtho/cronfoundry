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

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

// seedRunWithRepo creates the full (org → repo → skill → schedule → run)
// chain and returns (runID, orgID, repoID). This is what clone-url tests
// need: the handler cross-checks run→schedule→skill.repo_id against the
// URL's repo_id, so a bearer bound to one run can only fetch that run's
// own repo's clone URL.
func seedRunWithRepo(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
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
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 's', '* * * * *', 'openai', 'gpt-4o-mini', '[]'::jsonb) RETURNING id`,
		orgID, skillID).Scan(&schedID))
	var runPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-1', 'pending', 'manual', 'hash-placeholder') RETURNING id`,
		orgID, schedID).Scan(&runPG))
	return uuid.UUID(runPG.Bytes), orgID, repoID
}

func TestCloneURL_MintsURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID, repoID := seedRunWithRepo(t, pool)

	// Fake GitHub token-exchange endpoint.
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_xyz",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer tokSrv.Close()

	privPEM, _ := githubtest.MustPrivateKey(t)
	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      "1",
		PrivateKey: privPEM,
		BaseURL:    tokSrv.URL,
		HTTPClient: tokSrv.Client(),
	})

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{
		Pool:          pool,
		Signer:        signer,
		Installations: cache,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET",
		ts.URL+"/internal/repos/"+uuid.UUID(repoID.Bytes).String()+"/clone-url", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body["url"], "x-access-token:ghs_xyz@github.com/acme/widgets.git")
}

func TestCloneURL_MissingRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Seed a run + its repo so the bearer passes middleware; request a
	// DIFFERENT (missing) repo_id so the handler's scope check fires.
	runID, orgID, _ := seedRunWithRepo(t, pool)

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

	missingRepo := uuid.New()
	req, _ := http.NewRequest("GET", ts.URL+"/internal/repos/"+missingRepo.String()+"/clone-url", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// The scope check (run.repo_id != URL repo_id) returns 403 before we
	// get to the 404 "repo not found" path. This is stricter — non-existent
	// and wrong-owner repo IDs are indistinguishable to a caller.
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCloneURL_BadRepoID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID, _ := seedRunWithRepo(t, pool)
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

	req, _ := http.NewRequest("GET", ts.URL+"/internal/repos/not-a-uuid/clone-url", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestCloneURL_RejectsCrossRunRepoAccess exercises the CRIT-1 fix: a bearer
// bound to run A cannot fetch run B's repo's clone URL even when both runs
// exist in the same database.
func TestCloneURL_RejectsCrossRunRepoAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// runA + its repoA.
	runA, orgID, _ := seedRunWithRepo(t, pool)

	// Second repo + skill + schedule + run under the same org.
	ctx := context.Background()
	var repoB, skillB, schedB, runB pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 43, 'acme', 'other', 'main') RETURNING id`, orgID).Scan(&repoB))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/b', 'b', 'sha-b', '{}'::jsonb) RETURNING id`,
		orgID, repoB).Scan(&skillB))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 's-b', '* * * * *', 'openai', 'gpt-4o-mini', '[]'::jsonb) RETURNING id`,
		orgID, skillB).Scan(&schedB))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-b', 'pending', 'manual', 'hash-b') RETURNING id`,
		orgID, schedB).Scan(&runB))

	// Sign a token for run A; attempt to fetch repo B's clone URL.
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

	req, _ := http.NewRequest("GET",
		ts.URL+"/internal/repos/"+uuid.UUID(repoB.Bytes).String()+"/clone-url", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
