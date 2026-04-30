package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secrets/server"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestSecretsHandler_ListEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(context.Background())
	require.NoError(t, err)

	masterKey := make([]byte, 32)
	store := server.NewEnvelopePostgresStore(pool, org.ID, masterKey)
	deps := webapi.Deps{
		MasterKey:     masterKey,
		OAuthClientID: "test",
		AdminLogins:   []string{"alice"},
		Queries:       q,
		Secrets:       store,
	}
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, deps)

	req := httptest.NewRequest("GET", "/api/secrets", nil)
	addTestSession(t, req, masterKey, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var rows []any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rows))
	require.Empty(t, rows)
}
