package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/githubtest"
)

// buildRepo creates a bare repo seeded with a cronfoundry.yaml + SKILL.md
// and returns (cloneURL, headSHA).
func buildRepo(t *testing.T, manifestYAML, skillMD string) (string, string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "bare.git")

	r, err := git.PlainInit(work, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "cronfoundry.yaml"), []byte(manifestYAML), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(work, "skills", "a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "skills", "a", "SKILL.md"), []byte(skillMD), 0o644))

	wt, err := r.Worktree()
	require.NoError(t, err)
	_, err = wt.Add(".")
	require.NoError(t, err)
	sha, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@e", When: time.Now()},
	})
	require.NoError(t, err)
	require.NoError(t, r.Storer.SetReference(plumbing.NewHashReference(
		plumbing.ReferenceName("refs/heads/main"), sha,
	)))
	_, err = git.PlainClone(bare, true, &git.CloneOptions{URL: work})
	require.NoError(t, err)
	return "file://" + bare, sha.String()
}

// fakeGHServer stubs the branch-head endpoint.
func fakeGHServer(t *testing.T, sha string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/repos/")
		assert.Contains(t, r.URL.Path, "/branches/main")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"commit": map[string]any{"sha": sha},
		})
	}))
}

// fakeTokenServer stubs the installation-token exchange.
func fakeTokenServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/app/installations/")
		assert.Contains(t, r.URL.Path, "/access_tokens")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_test",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
}

func TestPoller_SyncOne_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	manifestYAML := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: hourly
        cron: "0 * * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	skillMD := `---
name: a
description: tiny
---
prompt
`

	cloneURL, sha := buildRepo(t, manifestYAML, skillMD)

	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	ctx := context.Background()

	ghSrv := fakeGHServer(t, sha)
	defer ghSrv.Close()
	tokSrv := fakeTokenServer(t)
	defer tokSrv.Close()

	privPEM, _ := githubtest.MustPrivateKey(t)

	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      "42",
		PrivateKey: privPEM,
		BaseURL:    tokSrv.URL,
		HTTPClient: tokSrv.Client(),
		Clock:      time.Now,
		TokenTTL:   50 * time.Minute,
	})

	p := NewPoller(PollerConfig{
		Pool:          pool,
		OrgID:         orgID,
		Installations: cache,
		GitHubBaseURL: ghSrv.URL,
		HTTPClient:    ghSrv.Client(),
		CloneURLFor: func(owner, name string) string {
			// In tests, route to the file:// fixture regardless of owner/name.
			return cloneURL
		},
	})

	require.NoError(t, p.SyncOne(ctx, repoID))

	// Assert: skill + schedule inserted.
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM skill WHERE repo_id = $1`, repoID).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM schedule`).Scan(&count))
	assert.Equal(t, 1, count)

	// last_synced_head_sha should be set.
	var gotSHA *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_synced_head_sha FROM repo_connection WHERE id = $1`, repoID).Scan(&gotSHA))
	require.NotNil(t, gotSHA)
	assert.Equal(t, sha, *gotSHA)

	// Second call: SHA unchanged, cheap path, last_synced_at advances.
	var firstSynced time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_synced_at FROM repo_connection WHERE id = $1`, repoID).Scan(&firstSynced))
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, p.SyncOne(ctx, repoID))
	var secondSynced time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_synced_at FROM repo_connection WHERE id = $1`, repoID).Scan(&secondSynced))
	assert.True(t, secondSynced.After(firstSynced))

	// Still one skill, one schedule (no duplicates from idempotent upsert).
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM skill WHERE repo_id = $1`, repoID).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM schedule`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestPoller_SyncOne_HeadCheckFailure_MarksError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()
	ctx := context.Background()

	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ghSrv.Close()
	tokSrv := fakeTokenServer(t)
	defer tokSrv.Close()

	privPEM, _ := githubtest.MustPrivateKey(t)
	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      "42",
		PrivateKey: privPEM,
		BaseURL:    tokSrv.URL,
		HTTPClient: tokSrv.Client(),
		Clock:      time.Now,
		TokenTTL:   50 * time.Minute,
	})

	p := NewPoller(PollerConfig{
		Pool:          pool,
		OrgID:         orgID,
		Installations: cache,
		GitHubBaseURL: ghSrv.URL,
		HTTPClient:    ghSrv.Client(),
	})

	err := p.SyncOne(ctx, repoID)
	require.Error(t, err)

	var lastErr *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_sync_error FROM repo_connection WHERE id = $1`, repoID).Scan(&lastErr))
	require.NotNil(t, lastErr)
	assert.Contains(t, *lastErr, "head check")
}
