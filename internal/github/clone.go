package github

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneAtSHA performs a shallow (depth=1) single-branch clone and checks out
// the specific commit SHA. installToken is the GitHub installation access
// token, used as basic-auth password. For file:// URLs (tests), pass "".
//
// destDir must be an empty (or nonexistent) directory. The function creates
// a fresh clone; it does not mutate an existing checkout.
func CloneAtSHA(ctx context.Context, cloneURL, installToken, sha, destDir string) error {
	opts := &git.CloneOptions{
		URL:          cloneURL,
		Depth:        1,
		SingleBranch: true,
	}
	if installToken != "" {
		opts.Auth = &githttp.BasicAuth{
			Username: "x-access-token",
			Password: installToken,
		}
	}
	repo, err := git.PlainCloneContext(ctx, destDir, false, opts)
	if err != nil {
		return fmt.Errorf("github: CloneAtSHA: clone %s: %w", cloneURL, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("github: CloneAtSHA: worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(sha)}); err != nil {
		return fmt.Errorf("github: CloneAtSHA: checkout %s: %w", sha, err)
	}
	return nil
}
