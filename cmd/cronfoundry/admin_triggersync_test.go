package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
)

func TestAdminTriggerSync_MissingEnv(t *testing.T) {
	t.Setenv(envDatabaseURL, "")
	t.Setenv(envGitHubAppID, "")
	t.Setenv(envGitHubAppPEM, "")
	err := runAdminTriggerSync(context.Background(), "o/r", &bytes.Buffer{})
	require.Error(t, err)
}

func TestAdminTriggerSync_MissingConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	t.Setenv(envGitHubAppID, "42")

	// Write a throwaway PEM to a temp file.
	priv, _ := github.MustTestPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, priv, 0o600))
	t.Setenv(envGitHubAppPEM, pemPath)

	require.NoError(t, runAdminInit(context.Background(), "o"))

	err := runAdminTriggerSync(context.Background(), "none/here", &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no connection")
}
