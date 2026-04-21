package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestSecrets_ScopedToClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Seed a real run so the bearer middleware's hash check passes, then
	// store two secrets on that run's org.
	runID, orgID := seedRun(t, pool)

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)
	require.NoError(t, store.Put(context.Background(), "allowed", "value-A"))
	require.NoError(t, store.Put(context.Background(), "forbidden", "value-F"))

	// Sign a token that only has "allowed" in its scope.
	signer := token.New(master)
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:      runID,
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"allowed"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{
		Pool:    pool,
		Signer:  signer,
		Secrets: store,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Allowed secret returns value.
	req, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=allowed", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "value-A", body["allowed"])

	// Forbidden secret → 403.
	req2, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=forbidden", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	resp2, err := ts.Client().Do(req2)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp2.StatusCode)
}

func TestSecrets_MultipleNames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)
	require.NoError(t, store.Put(context.Background(), "slack", "A"))
	require.NoError(t, store.Put(context.Background(), "openai", "B"))

	signer := token.New(master)
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:      runID,
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"slack", "openai"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer, Secrets: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=slack,openai", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "A", body["slack"])
	assert.Equal(t, "B", body["openai"])
}

func TestSecrets_MissingNamesParam(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/secrets", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSecrets_LogsFetchedEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)
	require.NoError(t, store.Put(context.Background(), "slack", "A"))
	require.NoError(t, store.Put(context.Background(), "openai", "B"))

	signer := token.New(master)
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:      runID,
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"slack", "openai"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer, Secrets: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=slack,openai", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ctx := context.Background()
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run_event WHERE run_id = $1 AND event_type = $2`,
		pgtype.UUID{Bytes: runID, Valid: true}, "secret.fetched").Scan(&count))
	assert.Equal(t, 2, count)

	// Verify both names appear in payloads.
	rows, err := pool.Query(ctx,
		`SELECT payload_json FROM run_event WHERE run_id = $1 AND event_type = $2 ORDER BY payload_json->>'name'`,
		pgtype.UUID{Bytes: runID, Valid: true}, "secret.fetched")
	require.NoError(t, err)
	defer rows.Close()
	var gotNames []string
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var p map[string]any
		require.NoError(t, json.Unmarshal(raw, &p))
		assert.Equal(t, true, p["in_manifest"])
		gotNames = append(gotNames, p["name"].(string))
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"openai", "slack"}, gotNames)
}

func TestSecrets_LogsDeniedEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)

	signer := token.New(master)
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:      runID,
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"allowed"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer, Secrets: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=forbidden", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	ctx := context.Background()
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run_event WHERE run_id = $1 AND event_type = $2`,
		pgtype.UUID{Bytes: runID, Valid: true}, "secret.denied").Scan(&count))
	assert.Equal(t, 1, count)

	var raw []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT payload_json FROM run_event WHERE run_id = $1 AND event_type = $2`,
		pgtype.UUID{Bytes: runID, Valid: true}, "secret.denied").Scan(&raw))
	var p map[string]any
	require.NoError(t, json.Unmarshal(raw, &p))
	assert.Equal(t, "forbidden", p["name"])
	assert.Equal(t, false, p["in_manifest"])
}
