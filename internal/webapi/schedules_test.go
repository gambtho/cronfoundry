package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
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
