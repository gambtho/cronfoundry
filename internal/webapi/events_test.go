package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestEventsHandler_ListInvalidID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	masterKey := make([]byte, 32)
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, testDeps(pool, masterKey))

	req := httptest.NewRequest("GET", "/api/runs/not-a-uuid/events", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}
