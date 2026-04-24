// Package writeback commits a <memory> block's content back to the skill
// repository using go-git. Push-to-remote is a separate method so commit is
// testable without network.
package writeback

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Options controls a single writeback commit.
type Options struct {
	Path        string // relative to repo root
	Mode        string // "append" | "replace"
	Content     string
	Message     string
	AuthorName  string
	AuthorEmail string
}

// Writer performs writeback commits and optional pushes.
type Writer struct{}

// New returns a Writer.
func New() *Writer { return &Writer{} }

// Commit applies the writeback content to `repoRoot/Path`, stages it, and
// commits. Returns the commit SHA.
func (w *Writer) Commit(repoRoot string, opts Options) (string, error) {
	if opts.Mode != "append" && opts.Mode != "replace" {
		return "", fmt.Errorf("writeback: mode %q invalid (want append|replace)", opts.Mode)
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("writeback: abs root: %w", err)
	}
	cleaned, err := filepath.Abs(filepath.Join(absRoot, opts.Path))
	if err != nil {
		return "", fmt.Errorf("writeback: resolve path: %w", err)
	}
	if !strings.HasPrefix(cleaned, absRoot+string(os.PathSeparator)) && cleaned != absRoot {
		return "", fmt.Errorf("writeback: path %q resolves outside repo", opts.Path)
	}

	var newContent string
	switch opts.Mode {
	case "replace":
		newContent = ensureTrailingNewline(opts.Content)
	case "append":
		existing, err := os.ReadFile(cleaned)
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("writeback: read existing: %w", err)
		}
		base := string(existing)
		if base != "" && !strings.HasSuffix(base, "\n") {
			base += "\n"
		}
		newContent = base + ensureTrailingNewline(opts.Content)
	}

	if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
		return "", fmt.Errorf("writeback: mkdir: %w", err)
	}
	if err := os.WriteFile(cleaned, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("writeback: write: %w", err)
	}

	repo, err := git.PlainOpen(absRoot)
	if err != nil {
		return "", fmt.Errorf("writeback: open repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("writeback: worktree: %w", err)
	}
	if _, err := wt.Add(opts.Path); err != nil {
		return "", fmt.Errorf("writeback: git add: %w", err)
	}
	msg := opts.Message
	if msg == "" {
		msg = fmt.Sprintf("chore(cronfoundry): update %s", opts.Path)
	}
	hash, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: opts.AuthorName, Email: opts.AuthorEmail, When: time.Now()},
	})
	if err != nil {
		return "", fmt.Errorf("writeback: commit: %w", err)
	}
	return hash.String(), nil
}

// Push sends the commit to the given remote using a PAT as basic-auth password.
// GitHub HTTPS push accepts any non-empty username.
func (w *Writer) Push(repoRoot, remoteName, username, token string) error {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("writeback: open repo: %w", err)
	}
	branch := "main"
	remote, err := repo.Remote(remoteName)
	if err == nil {
		for _, ref := range remote.Config().Fetch {
			if s := ref.Src(); strings.HasPrefix(s, "refs/heads/") {
				branch = strings.TrimPrefix(s, "refs/heads/")
				break
			}
		}
	}
	err = repo.Push(&git.PushOptions{
		RemoteName: remoteName,
		Auth:       &http.BasicAuth{Username: username, Password: token},
		RefSpecs:   []config.RefSpec{config.RefSpec("HEAD:refs/heads/" + branch)},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("writeback: push: %w", err)
	}
	return nil
}

// PushToURL pushes the current HEAD to the given authenticated remote URL.
// Used by the /internal/writeback-push endpoint so the server can push
// using a short-lived install token without exposing it to the runner.
func (w *Writer) PushToURL(repoRoot, remoteURL string) error {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("writeback: open repo: %w", err)
	}
	if err := repo.Push(&git.PushOptions{
		RemoteURL: remoteURL,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("writeback: push: %w", err)
	}
	return nil
}

func ensureTrailingNewline(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}
