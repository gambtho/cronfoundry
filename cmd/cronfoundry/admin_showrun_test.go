package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminShowRun_MissingRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	err := runAdminShowRun(context.Background(), "00000000-0000-0000-0000-000000000000", &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAdminShowRun_BadUUID(t *testing.T) {
	err := runAdminShowRun(context.Background(), "not-a-uuid", &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
