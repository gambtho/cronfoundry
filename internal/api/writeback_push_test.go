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

// TestWritebackPush_RejectsMismatchedRunID verifies 403 when the JWT run_id
// doesn't match the URL parameter.
func TestWritebackPush_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	// Seed a second run so the URL path differs from the JWT claim.
	runID2, _ := seedRunInOrg(t, pool, orgID)

	signer := token.New(randomMaster(t))
	// Sign for runID but request runID2's URL — middleware passes (runID exists),
	// handler fires the mismatch guard.
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	privPEM, _ := githubtest.MustPrivateKey(t)
	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID: "1", PrivateKey: privPEM,
	})
	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer, Installations: cache})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"commit_sha": "abc123", "repo_root": t.TempDir()})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID2.String()+"/writeback-push",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
