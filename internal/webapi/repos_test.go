package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// seedOrg seeds a minimal org and returns its pool for use in tests.
func seedOrg(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO organization (name) VALUES ('test-org')`)
	require.NoError(t, err)
}

// addTestSession signs and attaches a test session cookie to the request.
func addTestSession(t *testing.T, r *http.Request, masterKey []byte, login, role string) {
	t.Helper()
	cookie, err := webapi.NewTestSessionCookie(masterKey, login, role)
	require.NoError(t, err)
	r.AddCookie(cookie)
}

// testDeps builds a minimal Deps for handler tests.
func testDeps(pool *pgxpool.Pool, masterKey []byte) webapi.Deps {
	return webapi.Deps{
		MasterKey:     masterKey,
		OAuthClientID: "test",
		AdminLogins:   []string{"alice"},
		ViewerLogins:  []string{"bob"},
		Queries:       dbgen.New(pool),
	}
}

func TestReposHandler_List(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/repos", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var repos []any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&repos))
	require.Empty(t, repos)
}

func TestReposHandler_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/repos", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}
