package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startTestPostgres boots a throwaway Postgres container and returns its DSN.
// Callers must invoke the returned cleanup func.
func startTestPostgres(t *testing.T) (dsn string, cleanup func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cronfoundry_test"),
		postgres.WithUsername("cronfoundry"),
		postgres.WithPassword("cronfoundry"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return connStr, func() {
		_ = container.Terminate(context.Background())
	}
}

func TestNewPool_Connects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, cleanup := startTestPostgres(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	assert.NoError(t, pool.Ping(ctx))
}

func TestNewPool_BadDSN(t *testing.T) {
	_, err := NewPool(context.Background(), "postgres://nobody:nothing@nowhere:5432/none?connect_timeout=1&sslmode=disable")
	require.Error(t, err)
}
