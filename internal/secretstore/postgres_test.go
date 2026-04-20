package secretstore

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
)

// setupStore brings up a throwaway Postgres, runs migrations, seeds an org,
// and returns a ready-to-use *EnvelopePostgresStore bound to that org.
func setupStore(t *testing.T) (store *EnvelopePostgresStore, cleanup func()) {
	t.Helper()
	pool, cleanup := testdb.BootPG(t)
	ctx := context.Background()

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ($1) RETURNING id`, "test-org").Scan(&orgID))

	master, err := parseMasterKey(mustMasterKey(t))
	require.NoError(t, err)

	store = NewEnvelopePostgresStore(pool, orgID, master)
	return store, cleanup
}

func mustMasterKey(t *testing.T) string {
	t.Helper()
	k, err := GenerateMasterKey()
	require.NoError(t, err)
	return k
}

func TestEnvelopePostgresStore_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	store, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, store.Put(ctx, "slack_webhook", "https://hooks.slack.com/xyz"))
	got, err := store.Get(ctx, "slack_webhook")
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/xyz", got)
}

func TestEnvelopePostgresStore_Overwrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	store, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, store.Put(ctx, "k", "v1"))
	require.NoError(t, store.Put(ctx, "k", "v2"))
	got, err := store.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "v2", got)
}

func TestEnvelopePostgresStore_GetMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	store, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	_, err := store.Get(ctx, "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestEnvelopePostgresStore_List(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	store, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, store.Put(ctx, "b", "vb"))
	require.NoError(t, store.Put(ctx, "a", "va"))
	require.NoError(t, store.Put(ctx, "c", "vc"))

	names, err := store.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, names)
}

func TestEnvelopePostgresStore_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	store, cleanup := setupStore(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, store.Put(ctx, "k", "v"))
	require.NoError(t, store.Delete(ctx, "k"))
	// Deleting a missing secret is not an error.
	require.NoError(t, store.Delete(ctx, "nope"))

	_, err := store.Get(ctx, "k")
	assert.ErrorIs(t, err, ErrNotFound)
}
