package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/secrets/server"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// TestServe_BootsAndHealthz verifies the full serve boot flow:
//   - loads env
//   - opens DB
//   - loads org (requires admin init to have run)
//   - starts API
//   - /healthz responds 200
//   - shuts down gracefully on ctx cancellation
func TestServe_BootsAndHealthz(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	t.Setenv(envGitHubAppID, "42")

	// Write a throwaway PEM.
	priv, _ := githubtest.MustPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, priv, 0o600))
	t.Setenv(envGitHubAppPEM, pemPath)

	t.Setenv(envOAuthClientID, "cid")
	t.Setenv(envOAuthClientSecret, "csec")
	t.Setenv(envAdminLogins, "alice")

	// Run admin init first so organization exists.
	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a less-common local port to avoid collision with anything else.
	addr := "127.0.0.1:18089"

	errCh := make(chan error, 1)
	go func() { errCh <- runServe(ctx, addr, 30*time.Second) }()

	waitForHealthz(t, addr, 5*time.Second)

	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "ok", string(body))

	// Shutdown.
	cancel()
	select {
	case err := <-errCh:
		// ctx.Canceled or nil are both acceptable outcomes for graceful shutdown.
		assert.True(t, err == nil || err == context.Canceled, "serve exited with: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down within 15s")
	}
}

func TestServe_MissingMasterKey(t *testing.T) {
	t.Setenv(envMasterKey, "")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envMasterKey)
}

func TestServe_MissingDatabaseURL(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envDatabaseURL)
}

func TestServe_MissingGitHubAppID(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "postgres://example")
	t.Setenv(envGitHubAppID, "")
	t.Setenv(envGitHubAppPEM, "/tmp/nonexistent.pem")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envGitHubAppID)
}

func TestBuildJobDispatcher_Local(t *testing.T) {
	t.Setenv("DISPATCHER", "")
	t.Setenv("AZURE_CAE_RESOURCE_GROUP", "")
	t.Setenv("AZURE_CAE_JOB_NAME", "")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	d, err := buildJobDispatcher()
	require.NoError(t, err)
	_, ok := d.(*cloud.SubprocessDispatcher)
	require.True(t, ok, "expected SubprocessDispatcher when Azure env vars are unset")
}

func TestBuildSecretStore_Local(t *testing.T) {
	t.Setenv("SECRET_STORE", "")
	t.Setenv("AZURE_KEYVAULT_URL", "")
	store, err := buildSecretStore(nil, pgtype.UUID{}, nil)
	require.NoError(t, err)
	_, ok := store.(*server.EnvelopePostgresStore)
	require.True(t, ok, "expected EnvelopePostgresStore when AZURE_KEYVAULT_URL is unset")
}

func TestServe_APIMe_WithSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	masterKey := mustMasterKey(t)
	t.Setenv(envMasterKey, masterKey)
	t.Setenv(envDatabaseURL, dsn)
	t.Setenv(envGitHubAppID, "42")

	priv, _ := githubtest.MustPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, priv, 0o600))
	t.Setenv(envGitHubAppPEM, pemPath)

	t.Setenv(envOAuthClientID, "cid")
	t.Setenv(envOAuthClientSecret, "csec")
	t.Setenv(envAdminLogins, "alice")

	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := "127.0.0.1:18090"
	errCh := make(chan error, 1)
	go func() { errCh <- runServe(ctx, addr, 30*time.Second) }()

	waitForHealthz(t, addr, 5*time.Second)

	masterBytes, err := server.ParseMasterKey(masterKey)
	require.NoError(t, err)
	cookie, err := webapi.SignSession(
		webapi.SessionClaims{Login: "alice", Role: "admin"},
		masterBytes,
		time.Hour,
	)
	require.NoError(t, err)

	req, _ := http.NewRequest("GET", "http://"+addr+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)

	var got map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "alice", got["login"])
	assert.Equal(t, "admin", got["role"])

	cancel()
	select {
	case err := <-errCh:
		assert.True(t, err == nil || err == context.Canceled)
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

func TestServe_MissingOAuthClientID(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "postgres://example")
	t.Setenv(envGitHubAppID, "42")
	t.Setenv(envGitHubAppPEM, "/tmp/nonexistent.pem")
	t.Setenv(envOAuthClientID, "")
	t.Setenv(envOAuthClientSecret, "csec")
	t.Setenv(envAdminLogins, "alice")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envOAuthClientID)
}

func TestServe_MissingAdminLogins(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "postgres://example")
	t.Setenv(envGitHubAppID, "42")
	t.Setenv(envGitHubAppPEM, "/tmp/nonexistent.pem")
	t.Setenv(envOAuthClientID, "cid")
	t.Setenv(envOAuthClientSecret, "csec")
	t.Setenv(envAdminLogins, "")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envAdminLogins)
}

// waitForHealthz polls /healthz at addr until it returns HTTP 200 or the
// timeout elapses. Fails the test on timeout.
func waitForHealthz(t testing.TB, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.FailNowf(t, "healthz never returned 200", "addr=%s timeout=%v", addr, timeout)
}
