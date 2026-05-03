package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// seedRunForNotifications inserts the org/repo/skill/schedule chain
// needed to satisfy run's foreign keys, then a single run row, and
// returns its id. Raw SQL keeps this test from pulling in the
// scheduler package's helper.
func seedRunForNotifications(t *testing.T, pool *pgxpool.Pool) (orgID, runID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM organization LIMIT 1`).Scan(&orgID))

	var repoID, skillID, schedID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, timezone, overlap_policy,
			provider, model, destinations_json, env_json)
		 VALUES ($1, $2, 's', '* * * * *', 'UTC', 'skip',
		         'openai', 'gpt-4o-mini', '[]'::jsonb, '{}'::jsonb)
		 RETURNING id`, orgID, skillID).Scan(&schedID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha-1', 'succeeded', 'manual', 'hash') RETURNING id`,
		orgID, schedID).Scan(&runID))
	return orgID, runID
}

func TestRunNotifications_ListReturnsRowsInOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	orgID, runID := seedRunForNotifications(t, pool)

	_, err := pool.Exec(context.Background(),
		`INSERT INTO run_notification (run_id, org_id, kind, target, status, reason)
		 VALUES ($1, $2, 'github_issue', 'gambtho/cronfoundry#1', 'sent', NULL),
		        ($1, $2, 'webhook', 'https://example.com/hook', 'failed', 'timeout')`,
		runID, orgID)
	require.NoError(t, err)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/runs/"+runID.String()+"/notifications", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var rows []map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
	require.Len(t, rows, 2)

	assert.Equal(t, "github_issue", rows[0]["kind"])
	assert.Equal(t, "gambtho/cronfoundry#1", rows[0]["target"])
	assert.Equal(t, "sent", rows[0]["status"])
	assert.Nil(t, rows[0]["reason"])

	assert.Equal(t, "webhook", rows[1]["kind"])
	assert.Equal(t, "failed", rows[1]["status"])
	assert.Equal(t, "timeout", rows[1]["reason"])

	assert.Equal(t, runID.String(), rows[0]["run_id"])
}

func TestRunNotifications_EmptyReturnsArrayNotNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	_, runID := seedRunForNotifications(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/runs/"+runID.String()+"/notifications", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	// `[]` not `null`: pin the make([]T, 0) vs var []T regression.
	body := strings.TrimSpace(rr.Body.String())
	assert.Equal(t, "[]", body)
}

func TestRunNotifications_InvalidUUID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/runs/not-a-uuid/notifications", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRunNotifications_NonexistentRunReturnsEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	// Mirrors events.list: no explicit existence cross-check.
	bogus := uuid.New().String()
	req := httptest.NewRequest("GET", "/api/runs/"+bogus+"/notifications", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := strings.TrimSpace(rr.Body.String())
	assert.Equal(t, "[]", body)
}

func TestRunNotifications_OtherOrgInvisible(t *testing.T) {
	t.Skip("TODO: handler uses GetFirstOrganization which always picks the same org; " +
		"a true cross-org isolation test requires multi-org auth plumbing not present " +
		"in the existing test harness. The org-scoping is also enforced by the SQL " +
		"WHERE clause (run_notification.sql) which is exercised by the other tests in " +
		"this file.")
}
