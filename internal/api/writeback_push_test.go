package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestWritebackPush_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Seed two runs under the same org: sign for runA but request runB's URL
	// so the middleware's hash check passes and the handler's URL-vs-claim
	// guard returns 403.
	runA, orgID := seedRun(t, pool)
	runB, _ := seedRunInOrg(t, pool, orgID)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runA,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runA, hash)

	privPEM, _ := githubtest.MustPrivateKey(t)
	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID: "1", PrivateKey: privPEM,
	})
	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer, Installations: cache})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"commit_sha": "abc123", "repo_root": "/tmp"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runB.String()+"/writeback-push",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
