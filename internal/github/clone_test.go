package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeBareFixtureRepo creates a bare repo on disk with a single commit on
// `main` and returns (cloneURL, commitSHA).
func makeBareFixtureRepo(t *testing.T) (cloneURL, sha string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "bare.git")

	wRepo, err := git.PlainInit(work, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "hello.txt"), []byte("hi\n"), 0o644))

	wt, err := wRepo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("hello.txt")
	require.NoError(t, err)
	hash, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@e", When: time.Now()},
	})
	require.NoError(t, err)

	// Ensure refs/heads/main points at the commit so SingleBranch clones
	// find it. go-git's default init branch is "master"; we add "main"
	// explicitly.
	mainRef := plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/main"), hash)
	require.NoError(t, wRepo.Storer.SetReference(mainRef))

	_, err = git.PlainClone(bare, true, &git.CloneOptions{URL: work})
	require.NoError(t, err)

	return "file://" + bare, hash.String()
}

func TestCloneAtSHA_Success(t *testing.T) {
	url, sha := makeBareFixtureRepo(t)
	dest := t.TempDir()
	// PlainClone refuses a non-empty directory; use a fresh subdir.
	destClone := filepath.Join(dest, "clone")

	err := CloneAtSHA(context.Background(), url, "" /* no auth for file:// */, sha, destClone)
	require.NoError(t, err)

	contents, err := os.ReadFile(filepath.Join(destClone, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hi\n", string(contents))
}

func TestCloneAtSHA_UnreachableURL(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nope")
	err := CloneAtSHA(context.Background(), "file:///nonexistent.git", "", "deadbeef", dest)
	require.Error(t, err)
}
