package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/testdb"
)

func bootPostgres(t *testing.T) (dsn string, teardown func()) {
	return testdb.BootPGWithDSN(t)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()

	fn()
	require.NoError(t, w.Close())
	<-done
	return buf.String()
}

func mustMasterKey(t *testing.T) string {
	t.Helper()
	k, err := secretstore.GenerateMasterKey()
	require.NoError(t, err)
	return k
}

func TestAdminInit_MissingMasterKey_PrintsAndExits(t *testing.T) {
	t.Setenv(envMasterKey, "")
	t.Setenv(envDatabaseURL, "") // should NOT be consulted in this branch
	out := captureStdout(t, func() {
		require.NoError(t, runAdminInit(context.Background(), "default"))
	})
	assert.Contains(t, out, envMasterKey+"=")
	assert.Contains(t, out, "re-run")
}

func TestAdminInit_MissingDatabaseURL_FailsAfterKeyIsSet(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "")
	err := runAdminInit(context.Background(), "default")
	require.Error(t, err)
	assert.Contains(t, err.Error(), envDatabaseURL)
}

func TestAdminInit_MigratesAndSeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)

	out := captureStdout(t, func() {
		require.NoError(t, runAdminInit(context.Background(), "test-org"))
	})
	assert.Contains(t, out, "Seeded organization")

	// Second run should be idempotent.
	out2 := captureStdout(t, func() {
		require.NoError(t, runAdminInit(context.Background(), "test-org"))
	})
	assert.Contains(t, out2, "already seeded")

	// Verify a row exists.
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM organization`).Scan(&count))
	assert.Equal(t, 1, count)
}
