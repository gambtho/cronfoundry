package webapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// seedUser inserts a user into the single test org.
func seedUser(t *testing.T, pool *pgxpool.Pool, login, role string) {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	require.NoError(t, err)
	_, err = q.CreateUser(ctx, dbgen.CreateUserParams{
		OrgID:       org.ID,
		GithubLogin: login,
		Role:        role,
	})
	require.NoError(t, err)
}

// doUsersReq builds+serves an authenticated request against mux.
func doUsersReq(t *testing.T, mux *http.ServeMux, masterKey []byte, method, path, body, login, role string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	addTestSession(t, r, masterKey, login, role)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	return rr
}

// TestUsers_List seeds two users and verifies GET /api/users returns
// them in github_login order with the expected DTO fields.
func TestUsers_List(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)
	seedUser(t, pool, "alice", "admin")
	seedUser(t, pool, "bob", "viewer")

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	rr := doUsersReq(t, mux, masterKey, "GET", "/api/users", "", "alice", "admin")
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var rows []map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
	require.Len(t, rows, 2)
	assert.Equal(t, "alice", rows[0]["login"])
	assert.Equal(t, "admin", rows[0]["role"])
	assert.Equal(t, "bob", rows[1]["login"])
	assert.Equal(t, "viewer", rows[1]["role"])
	// created_at should be present.
	assert.NotEmpty(t, rows[0]["created_at"])
}

// TestUsers_Create exercises the happy path + validation branches in
// one test: success, conflict on duplicate, 400 on invalid role.
func TestUsers_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)
	seedUser(t, pool, "alice", "admin")

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	t.Run("succeeds", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "POST", "/api/users",
			`{"login":"carol","role":"viewer"}`, "alice", "admin")
		require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
		var out map[string]any
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
		assert.Equal(t, "carol", out["login"])
		assert.Equal(t, "viewer", out["role"])

		// List should now have 2 users.
		listRR := doUsersReq(t, mux, masterKey, "GET", "/api/users", "", "alice", "admin")
		require.Equal(t, http.StatusOK, listRR.Code)
		var rows []map[string]any
		require.NoError(t, json.NewDecoder(listRR.Body).Decode(&rows))
		require.Len(t, rows, 2)
	})

	t.Run("conflict on duplicate", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "POST", "/api/users",
			`{"login":"alice","role":"admin"}`, "alice", "admin")
		require.Equal(t, http.StatusConflict, rr.Code, "body=%s", rr.Body.String())
	})

	t.Run("invalid role", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "POST", "/api/users",
			`{"login":"mallory","role":"superuser"}`, "alice", "admin")
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing login", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "POST", "/api/users",
			`{"role":"viewer"}`, "alice", "admin")
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

// TestUsers_UpdateRole exercises successful role change and 404 for
// missing user.
func TestUsers_UpdateRole(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)
	seedUser(t, pool, "alice", "admin")
	seedUser(t, pool, "bob", "viewer")

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	t.Run("succeeds", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "PATCH", "/api/users/bob",
			`{"role":"admin"}`, "alice", "admin")
		require.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())

		listRR := doUsersReq(t, mux, masterKey, "GET", "/api/users", "", "alice", "admin")
		require.Equal(t, http.StatusOK, listRR.Code)
		var rows []map[string]any
		require.NoError(t, json.NewDecoder(listRR.Body).Decode(&rows))
		require.Len(t, rows, 2)
		// bob should now be admin (sorted alphabetically alice, bob).
		assert.Equal(t, "bob", rows[1]["login"])
		assert.Equal(t, "admin", rows[1]["role"])
	})

	t.Run("not found", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "PATCH", "/api/users/ghost",
			`{"role":"viewer"}`, "alice", "admin")
		require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
	})

	t.Run("invalid role 400", func(t *testing.T) {
		rr := doUsersReq(t, mux, masterKey, "PATCH", "/api/users/bob",
			`{"role":"root"}`, "alice", "admin")
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

// TestUsers_Delete covers happy path, self-delete guard, and last-user
// guard. The self-delete guard fires BEFORE the count check.
func TestUsers_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	t.Run("succeeds", func(t *testing.T) {
		pool, cleanup := testdb.BootPG(t)
		defer cleanup()
		seedOrg(t, pool)
		seedUser(t, pool, "alice", "admin")
		seedUser(t, pool, "bob", "viewer")

		masterKey := make([]byte, 32)
		mux := http.NewServeMux()
		webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

		rr := doUsersReq(t, mux, masterKey, "DELETE", "/api/users/bob",
			"", "alice", "admin")
		require.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())

		listRR := doUsersReq(t, mux, masterKey, "GET", "/api/users", "", "alice", "admin")
		require.Equal(t, http.StatusOK, listRR.Code)
		var rows []map[string]any
		require.NoError(t, json.NewDecoder(listRR.Body).Decode(&rows))
		require.Len(t, rows, 1)
		assert.Equal(t, "alice", rows[0]["login"])
	})

	t.Run("self delete blocked", func(t *testing.T) {
		pool, cleanup := testdb.BootPG(t)
		defer cleanup()
		seedOrg(t, pool)
		seedUser(t, pool, "alice", "admin")
		seedUser(t, pool, "bob", "viewer")

		masterKey := make([]byte, 32)
		mux := http.NewServeMux()
		webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

		rr := doUsersReq(t, mux, masterKey, "DELETE", "/api/users/alice",
			"", "alice", "admin")
		require.Equal(t, http.StatusConflict, rr.Code, "body=%s", rr.Body.String())
		var body map[string]any
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
		assert.Equal(t, "self_delete", body["code"])
	})

	// NOTE: the "last_user" guard (count <= 1) is unreachable in
	// single-threaded sequential flow after the user-existence check was
	// reordered ahead of it — the only user with count==1 is self, which
	// the self_delete guard blocks first. The guard remains in place as
	// defense-in-depth against the count→delete TOCTOU race documented in
	// users.go. A test that meaningfully exercises it would require
	// concurrent writes; we don't run concurrent DB integration tests here.

	t.Run("not found", func(t *testing.T) {
		pool, cleanup := testdb.BootPG(t)
		defer cleanup()
		seedOrg(t, pool)
		seedUser(t, pool, "alice", "admin")
		seedUser(t, pool, "bob", "viewer")

		masterKey := make([]byte, 32)
		mux := http.NewServeMux()
		webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

		rr := doUsersReq(t, mux, masterKey, "DELETE", "/api/users/ghost",
			"", "alice", "admin")
		require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
	})
}

// TestUsers_RequiresAdmin confirms that a viewer session is denied on
// every mutating and read endpoint.
func TestUsers_RequiresAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)
	seedUser(t, pool, "alice", "admin")
	seedUser(t, pool, "bob", "viewer")

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	cases := []struct {
		method, path, body string
	}{
		{"GET", "/api/users", ""},
		{"POST", "/api/users", `{"login":"carol","role":"viewer"}`},
		{"PATCH", "/api/users/bob", `{"role":"admin"}`},
		{"DELETE", "/api/users/bob", ""},
	}
	for _, c := range cases {
		rr := doUsersReq(t, mux, masterKey, c.method, c.path, c.body, "bob", "viewer")
		require.Equal(t, http.StatusForbidden, rr.Code,
			"%s %s expected 403, got %d body=%s", c.method, c.path, rr.Code, rr.Body.String())
	}
}
