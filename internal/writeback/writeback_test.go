package writeback

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signature(name, email string) *object.Signature {
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}

func TestCommit_AppendsAndCommits(t *testing.T) {
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	require.NoError(t, err)

	// Seed: create memory.md and commit it.
	memPath := filepath.Join(root, "memory.md")
	require.NoError(t, os.WriteFile(memPath, []byte("existing line\n"), 0o644))
	wt, _ := repo.Worktree()
	_, _ = wt.Add("memory.md")
	_, err = wt.Commit("seed", &git.CommitOptions{
		Author: signature("seed", "seed@example.com"),
	})
	require.NoError(t, err)

	w := New()
	commitSHA, err := w.Commit(root, Options{
		Path:        "memory.md",
		Mode:        "append",
		Content:     "new line",
		Message:     "chore(cronfoundry): update memory.md",
		AuthorName:  "cronfoundry[bot]",
		AuthorEmail: "cronfoundry[bot]@users.noreply.github.com",
	})
	require.NoError(t, err)
	assert.Len(t, commitSHA, 40)

	got, err := os.ReadFile(memPath)
	require.NoError(t, err)
	assert.Equal(t, "existing line\nnew line\n", string(got))
}

func TestCommit_Replace(t *testing.T) {
	root := t.TempDir()
	_, err := git.PlainInit(root, false)
	require.NoError(t, err)

	w := New()
	_, err = w.Commit(root, Options{
		Path:        "memory.md",
		Mode:        "replace",
		Content:     "fresh content",
		Message:     "msg",
		AuthorName:  "a",
		AuthorEmail: "a@b",
	})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(root, "memory.md"))
	require.NoError(t, err)
	assert.Equal(t, "fresh content\n", string(got))
}

func TestCommit_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	_, err := git.PlainInit(root, false)
	require.NoError(t, err)

	w := New()
	_, err = w.Commit(root, Options{
		Path: "../escape.md", Mode: "append", Content: "x",
		AuthorName: "a", AuthorEmail: "a@b",
	})
	assert.ErrorContains(t, err, "outside")
}

func TestCommit_UnknownMode(t *testing.T) {
	root := t.TempDir()
	_, err := git.PlainInit(root, false)
	require.NoError(t, err)
	w := New()
	_, err = w.Commit(root, Options{Path: "m.md", Mode: "delete", Content: "x", AuthorName: "a", AuthorEmail: "a@b"})
	assert.ErrorContains(t, err, "mode")
}
