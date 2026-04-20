package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/githubtest"
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

	// Run admin init first so organization exists.
	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a less-common local port to avoid collision with anything else.
	addr := "127.0.0.1:18089"

	errCh := make(chan error, 1)
	go func() { errCh <- runServe(ctx, addr, 30*time.Second) }()

	// Poll /healthz until up, or 5s deadline.
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	var healthErr error
	for time.Now().Before(deadline) {
		resp, healthErr = http.Get("http://" + addr + "/healthz")
		if healthErr == nil && resp.StatusCode == 200 {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, resp, "healthz never responded; err=%v", healthErr)
	defer resp.Body.Close()
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
