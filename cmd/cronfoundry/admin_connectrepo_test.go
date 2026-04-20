package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminConnectRepo_MissingDatabaseURL(t *testing.T) {
	t.Setenv(envDatabaseURL, "")
	err := runAdminConnectRepo(context.Background(), "o/r", 1, "main", 60, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), envDatabaseURL)
}

func TestAdminConnectRepo_BadRepoFormat(t *testing.T) {
	t.Setenv(envDatabaseURL, "postgres://example")
	err := runAdminConnectRepo(context.Background(), "not-a-slash", 1, "main", 60, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner/name")
}

func TestAdminConnectRepo_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	var buf bytes.Buffer
	err := runAdminConnectRepo(context.Background(), "myorg/myrepo", 42, "main", 60, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Connected myorg/myrepo")

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	defer pool.Close()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM repo_connection WHERE owner='myorg' AND name='myrepo'`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestAdminConnectRepo_Idempotent(t *testing.T) {
	// Second call with the same owner/name updates install-id (ON CONFLICT DO UPDATE), not inserts.
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	require.NoError(t, runAdminConnectRepo(context.Background(), "myorg/myrepo", 42, "main", 60, &bytes.Buffer{}))
	require.NoError(t, runAdminConnectRepo(context.Background(), "myorg/myrepo", 99, "develop", 120, &bytes.Buffer{}))

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	defer pool.Close()

	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM repo_connection WHERE owner='myorg' AND name='myrepo'`).Scan(&count))
	assert.Equal(t, 1, count, "second call should UPDATE, not INSERT")

	var installID int64
	var branch string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT github_app_install_id, default_branch FROM repo_connection WHERE owner='myorg' AND name='myrepo'`).Scan(&installID, &branch))
	assert.Equal(t, int64(99), installID)
	assert.Equal(t, "develop", branch)
}
