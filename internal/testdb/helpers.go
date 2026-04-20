// Package testdb provides shared test helpers for booting a throwaway
// Postgres container and running migrations.
//
// This package imports `testing`, so it must be imported only from
// `*_test.go` files.
package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/db"
)

// BootPG starts a throwaway Postgres 16 container, runs all migrations, and
// returns a ready-to-use pgx pool + a cleanup func the caller must defer.
func BootPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn, teardown := BootPGWithDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		teardown()
	}
}

// BootPGWithDSN is like BootPG but returns the DSN instead of a pool.
// Intended for callers that want to open their own pool (e.g., CLI tests
// that exercise pgxpool.New from inside the code under test).
func BootPGWithDSN(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf"),
		postgres.WithUsername("cf"),
		postgres.WithPassword("cf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, dsn))

	return dsn, func() { _ = container.Terminate(context.Background()) }
}
