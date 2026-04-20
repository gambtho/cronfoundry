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
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestSecrets_ScopedToClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	// Seed an org and store two secrets.
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)
	require.NoError(t, store.Put(context.Background(), "allowed", "value-A"))
	require.NoError(t, store.Put(context.Background(), "forbidden", "value-F"))

	// Sign a token that only has "allowed" in its scope.
	signer := token.New(master)
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:      uuid.New(),
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"allowed"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp2.StatusCode)
}

func TestSecrets_MultipleNames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)
	require.NoError(t, store.Put(context.Background(), "slack", "A"))
	require.NoError(t, store.Put(context.Background(), "openai", "B"))

	signer := token.New(master)
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:      uuid.New(),
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"slack", "openai"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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
	pool, cleanup := bootPG(t)
	defer cleanup()

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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
