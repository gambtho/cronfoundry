// internal/bootstrap/azure/deploy_test.go
package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploy_InvokesAzWithExpectedArgs(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	require.NoError(t, Deploy(context.Background(), mr,
		"swedencentral", "deploy/main.bicep", "deploy/params.prod.json"))
	require.Len(t, mr.Calls, 1)
	require.Equal(t, "az", mr.Calls[0].Name)
	require.Equal(t, []string{
		"deployment", "sub", "create",
		"--location", "swedencentral",
		"--template-file", "deploy/main.bicep",
		"--parameters", "@deploy/params.prod.json",
	}, mr.Calls[0].Args)
}

func TestAllowOperatorIP_CallsAzWithIP(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	require.NoError(t, AllowOperatorIP(context.Background(), mr, "prod", "203.0.113.7"))
	require.Len(t, mr.Calls, 1)
	args := mr.Calls[0].Args
	require.Contains(t, args, "rg-cronfoundry-prod")
	require.Contains(t, args, "cf-pg-prod")
	require.Contains(t, args, "203.0.113.7")
}

func TestRestartServe_SetsRestartTrigger(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	require.NoError(t, RestartServe(context.Background(), mr, "prod"))
	require.Len(t, mr.Calls, 1)
	args := mr.Calls[0].Args
	require.Contains(t, args, "--name")
	require.Contains(t, args, "cf-serve-prod")
	var found bool
	for _, a := range args {
		if strings.HasPrefix(a, "RESTART_TRIGGER=") {
			found = true
		}
	}
	require.True(t, found, "expected RESTART_TRIGGER=... in args, got %v", args)
}
