package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminSetSecret_MissingMasterKey(t *testing.T) {
	t.Setenv(envMasterKey, "")
	t.Setenv(envDatabaseURL, "postgres://example")
	err := runAdminSetSecret(context.Background(), "n",
		strings.NewReader("value\n"), &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), envMasterKey)
}

func TestAdminSetSecret_EmptyValue(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "postgres://example")
	err := runAdminSetSecret(context.Background(), "n",
		strings.NewReader("\n"), &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty value")
}

func TestAdminSetSecret_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)

	// Bootstrap: init first.
	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	out := &bytes.Buffer{}
	require.NoError(t, runAdminSetSecret(
		context.Background(), "slack_webhook",
		strings.NewReader("https://hooks.slack.com/xyz\n"), out))
	assert.Contains(t, out.String(), "Stored secret")
}
