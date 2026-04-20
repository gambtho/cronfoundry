package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminListConnections_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	var buf bytes.Buffer
	require.NoError(t, runAdminListConnections(context.Background(), &buf))
	assert.Contains(t, buf.String(), "no connected repos")
}

func TestAdminListSchedules_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	var buf bytes.Buffer
	require.NoError(t, runAdminListSchedules(context.Background(), &buf))
	assert.Contains(t, buf.String(), "no schedules")
}

func TestAdminListConnections_ShowsRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))
	require.NoError(t, runAdminConnectRepo(context.Background(), "foo/bar", 7, "main", 60, &bytes.Buffer{}))

	var buf bytes.Buffer
	require.NoError(t, runAdminListConnections(context.Background(), &buf))
	assert.Contains(t, buf.String(), "foo/bar")
	assert.Contains(t, buf.String(), "never")
}
