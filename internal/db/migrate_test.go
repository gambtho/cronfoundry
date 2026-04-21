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

func TestMigrate_AllTablesCreated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, cleanup := startTestPostgres(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, Migrate(ctx, dsn))

	pool, err := NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	expected := []string{
		"organization", "repo_connection", "skill", "schedule",
		"run", "run_event", "secret", "audit_log", "app_user",
	}
	for _, table := range expected {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables
			               WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		require.NoError(t, err, "checking %s", table)
		assert.True(t, exists, "table %s should exist after Migrate", table)
	}

	// Verify the partial-unique-index on run(schedule_id, fire_time) exists.
	var indexExists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_indexes
		               WHERE schemaname='public' AND indexname='run_schedule_firetime_unique')`,
	).Scan(&indexExists)
	require.NoError(t, err)
	assert.True(t, indexExists, "run_schedule_firetime_unique index should exist")

	// Verify goose advanced past version 0.
	var version int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = true`).Scan(&version))
	assert.Greater(t, version, int64(0), "goose should show at least one applied migration")
}
