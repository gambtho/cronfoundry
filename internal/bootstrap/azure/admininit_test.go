// internal/bootstrap/azure/admininit_test.go
package azure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminInit_PassesEnvVars(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	dsn := "postgres://cfadmin:pw@cf-pg-prod.postgres.database.azure.com:5432/cronfoundry?sslmode=require"
	require.NoError(t, AdminInit(context.Background(), mr, "/path/to/cronfoundry", dsn, "MK", "default"))
	require.Len(t, mr.EnvCalls, 1)
	c := mr.EnvCalls[0]
	require.Equal(t, "/path/to/cronfoundry", c.Name)
	require.Contains(t, c.Args, "admin")
	require.Contains(t, c.Args, "init")
	require.Contains(t, c.Env, "CRONFOUNDRY_DATABASE_URL="+dsn)
	require.Contains(t, c.Env, "CRONFOUNDRY_MASTER_KEY=MK")
}
