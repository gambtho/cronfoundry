// internal/bootstrap/azure/bootstrap_test.go
package azure

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrap_DryRun_StopsAfterParamsWrite(t *testing.T) {
	dir := t.TempDir()
	paramsPath := filepath.Join(dir, "params.json")
	imageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer imageSrv.Close()

	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},   // az account show
		{Stdout: []byte("Bicep CLI 0.30")}, // az bicep version
	}}

	bs := &Bootstrap{
		Runner:       mr,
		Inputs:       goodInputs(),
		MasterKey:    "MK",
		ParamsPath:   paramsPath,
		TemplateFile: "deploy/main.bicep",
		DryRun:       true,
		ImageRoot:    imageSrv.URL,
		Stdout:       &bytes.Buffer{},
	}
	require.NoError(t, bs.Run(context.Background()))

	require.Len(t, mr.Calls, 2)
	for _, c := range mr.Calls {
		require.NotContains(t, strings.Join(c.Args, " "), "deployment sub create")
	}
}

func TestBootstrap_HappyPath_NonDryRun(t *testing.T) {
	dir := t.TempDir()
	paramsPath := filepath.Join(dir, "params.json")

	imageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer imageSrv.Close()

	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/healthz", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer healthSrv.Close()
	healthHost := strings.TrimPrefix(healthSrv.URL, "http://")

	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},                            // az account show
		{Stdout: []byte("Bicep CLI 0.30")},                          // az bicep version
		{},                                                          // az deployment sub create (RunStreaming)
		{},                                                          // az postgres firewall-rule create
		{},                                                          // <binary> admin init (RunWithEnv)
		{},                                                          // az containerapp update --set-env-vars
		{Stdout: []byte("cf-serve-prod.example.azurecontainerapps.io\n")}, // az containerapp show
	}}

	ipDetectorCalls := 0
	stdout := &bytes.Buffer{}
	bs := &Bootstrap{
		Runner:       mr,
		Inputs:       goodInputs(),
		MasterKey:    "MK",
		ParamsPath:   paramsPath,
		TemplateFile: "deploy/main.bicep",
		Binary:       "/usr/local/bin/cronfoundry",
		ImageRoot:    imageSrv.URL,
		HealthScheme: "http",
		HealthHost:   healthHost,
		IPDetector: func(ctx context.Context) (string, error) {
			ipDetectorCalls++
			return "203.0.113.7", nil
		},
		Stdout: stdout,
	}

	require.NoError(t, bs.Run(context.Background()))
	require.Equal(t, 1, ipDetectorCalls)

	// admin init env assertions
	require.Len(t, mr.EnvCalls, 1)
	envStr := strings.Join(mr.EnvCalls[0].Env, "\n")
	require.Contains(t, envStr, "CRONFOUNDRY_DATABASE_URL=")
	require.Contains(t, envStr, "CRONFOUNDRY_MASTER_KEY=MK")
	require.Equal(t, "/usr/local/bin/cronfoundry", mr.EnvCalls[0].Name)
	require.Equal(t, []string{"admin", "init", "--org-name", "default"}, mr.EnvCalls[0].Args)

	// containerapp update with RESTART_TRIGGER must have been issued
	var sawRestart bool
	for _, c := range mr.Calls {
		joined := strings.Join(c.Args, " ")
		if strings.Contains(joined, "RESTART_TRIGGER=") {
			sawRestart = true
		}
	}
	require.True(t, sawRestart, "expected containerapp update with RESTART_TRIGGER")

	// stdout mentions discovered fqdn (the show output)
	require.Contains(t, stdout.String(), "cf-serve-prod.example.azurecontainerapps.io")
}
