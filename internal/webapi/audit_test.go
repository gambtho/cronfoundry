package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/audit"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// TestAudit_RequiresAdmin confirms that non-admin callers cannot read
// the audit log. The adminOnly middleware should return 403 for a
// viewer session.
func TestAudit_RequiresAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/audit", nil)
	addTestSession(t, req, masterKey, "bob", "viewer")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
}

// TestAudit_ListReturnsRowsDESC seeds three audit rows in forward time
// order and confirms the API returns them newest-first, and that the
// detail_json round-trips as a nested object (not a string).
func TestAudit_ListReturnsRowsDESC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	require.NoError(t, err)

	for _, actor := range []string{"u1", "u2", "u3"} {
		require.NoError(t, audit.Log(ctx, q, audit.Entry{
			OrgID:  org.ID,
			Actor:  actor,
			Action: "test.action",
			Detail: map[string]any{"who": actor, "n": 1},
		}))
	}

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/audit", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var rows []map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
	require.Len(t, rows, 3)

	// Newest-first: u3 should be first.
	assert.Equal(t, "u3", rows[0]["actor"])
	assert.Equal(t, "u2", rows[1]["actor"])
	assert.Equal(t, "u1", rows[2]["actor"])

	// detail must be a nested object, not a string.
	detail, ok := rows[0]["detail"].(map[string]any)
	require.True(t, ok, "detail should decode to object, got %T", rows[0]["detail"])
	assert.Equal(t, "u3", detail["who"])
}

// TestAudit_LimitAndOffset seeds 5 rows then verifies that limit and
// offset parameters page through the result set. Also checks that a
// limit above auditMaxLimit doesn't error (5 rows is well under 500).
func TestAudit_LimitAndOffset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	require.NoError(t, err)

	for _, actor := range []string{"a", "b", "c", "d", "e"} {
		require.NoError(t, audit.Log(ctx, q, audit.Entry{
			OrgID:  org.ID,
			Actor:  actor,
			Action: "test.action",
		}))
	}

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	get := func(path string) []map[string]any {
		req := httptest.NewRequest("GET", path, nil)
		addTestSession(t, req, masterKey, "alice", "admin")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, "path=%s body=%s", path, rr.Body.String())
		var rows []map[string]any
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
		return rows
	}

	page1 := get("/api/audit?limit=2")
	require.Len(t, page1, 2)
	// Newest first: e, d
	assert.Equal(t, "e", page1[0]["actor"])
	assert.Equal(t, "d", page1[1]["actor"])

	page2 := get("/api/audit?limit=2&offset=2")
	require.Len(t, page2, 2)
	// Third and fourth newest: c, b
	assert.Equal(t, "c", page2[0]["actor"])
	assert.Equal(t, "b", page2[1]["actor"])

	// An outrageous limit should be accepted and capped server-side;
	// with only 5 rows we just assert it returns all of them.
	all := get("/api/audit?limit=9999")
	require.Len(t, all, 5)
}
