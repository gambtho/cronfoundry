package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootPG_ReturnsReadyPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := BootPG(t)
	defer cleanup()

	ctx := context.Background()
	assert.NoError(t, pool.Ping(ctx))

	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
                       WHERE table_schema='public' AND table_name='organization')`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestBootPGWithDSN_ReturnsWorkingDSN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, cleanup := BootPGWithDSN(t)
	defer cleanup()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	assert.NoError(t, pool.Ping(ctx))
}
