package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate_IsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, cleanup := startTestPostgres(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First run — applies all migrations (none yet, but the call must succeed).
	require.NoError(t, Migrate(ctx, dsn))
	// Second run — idempotent no-op.
	require.NoError(t, Migrate(ctx, dsn))

	pool, err := NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// goose_db_version table must exist after Migrate runs.
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM information_schema.tables
		 WHERE table_schema='public' AND table_name='goose_db_version'`).Scan(&count))
	assert.Equal(t, 1, count, "goose_db_version table should exist after Migrate")
}
