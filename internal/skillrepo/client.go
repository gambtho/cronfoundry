// Package skillrepo composes go-github calls into the specific "open a PR
// with a single-file change" pipeline used by POST /api/skill-repo/jobs.
// Lower-level transport and JWT/install-token primitives live in
// internal/github; skillrepo is the call-flow layer above them.
//
// Token minting is plug-in via TokenFunc — production wires
// internal/github.InstallationCache.Token, tests inject a stub.
package skillrepo

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v74/github"
)

// ErrPermissionRequired is returned when the GitHub App installation lacks
// a permission needed to complete the operation (e.g. pull_requests:write).
// Callers map this to HTTP 412.
var ErrPermissionRequired = errors.New("skillrepo: github app missing required permission")

// ErrConflict is returned for 409/422 responses on branch creation
// (already exists), file PUT (sha mismatch), or PR open (PR already exists
// for branch). Callers map this to HTTP 409.
var ErrConflict = errors.New("skillrepo: github reported a conflict")

// ErrFileNotFound is returned when GetFile receives a 404 (missing
// cronfoundry.yaml on the default branch). Callers map this to HTTP 400
// with a clear message.
var ErrFileNotFound = errors.New("skillrepo: file not found on default branch")

// FileContents bundles what GetFile returns: file blob + the head commit
// sha of the branch the file is on. The caller passes the file sha back
// to PutFile and the head sha to CreateBranch.
type FileContents struct {
	Content []byte
	FileSHA string // for "If-Match"-style PUT
	HeadSHA string // commit sha the file was read at
}

// PRRequest is the structured input to CreatePR.
type PRRequest struct {
	Owner  string
	Repo   string
	Branch string
	Base   string
	Title  string
	Body   string
}

// PRResult is what CreatePR returns on success.
type PRResult struct {
	HTMLURL string
	Number  int
}

// TokenFunc returns an installation token for installID. Wired in
// production from internal/github.InstallationCache.Token; tests inject
// a stub.
type TokenFunc func(ctx context.Context, installID int64) (string, error)

// Client is the stateless wrapper. Each method mints a fresh install token
// via Token and constructs a per-call go-github client. We don't cache
// *gh.Client because token refresh would invalidate it.
type Client struct {
	Token      TokenFunc
	BaseURL    string
	HTTPClient *http.Client
}

// New constructs a Client. baseURL defaults to "https://api.github.com" if empty.
func New(token TokenFunc, baseURL string) *Client {
	return &Client{Token: token, BaseURL: baseURL}
}

// gitHubClient mints an install token and returns a configured go-github
// client. Internal helper.
func (c *Client) gitHubClient(ctx context.Context, installID int64) (*gh.Client, error) {
	tok, err := c.Token(ctx, installID)
	if err != nil {
		return nil, fmt.Errorf("skillrepo: token: %w", err)
	}
	httpClient := c.HTTPClient
	cli := gh.NewClient(httpClient).WithAuthToken(tok)
	if c.BaseURL != "" {
		u := c.BaseURL
		if u[len(u)-1] != '/' {
			u += "/"
		}
		base, err := cli.BaseURL.Parse(u)
		if err != nil {
			return nil, fmt.Errorf("skillrepo: parse base url: %w", err)
		}
		cli.BaseURL = base
	}
	return cli, nil
}

// GetFile fetches a file at the named ref. Returns ErrFileNotFound on 404.
func (c *Client) GetFile(ctx context.Context, installID int64, owner, repo, path, ref string) (*FileContents, error) {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return nil, err
	}
	opts := &gh.RepositoryContentGetOptions{Ref: ref}
	fileC, _, resp, err := cli.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("skillrepo: GetContents: %w", err)
	}
	if fileC == nil {
		// GetContents returns dirContents when path is a directory; we want a file.
		return nil, fmt.Errorf("skillrepo: %s is not a file", path)
	}
	content, err := fileC.GetContent()
	if err != nil {
		return nil, fmt.Errorf("skillrepo: decode content: %w", err)
	}
	// Look up the head commit sha of ref.
	branch, _, err := cli.Repositories.GetBranch(ctx, owner, repo, ref, 0)
	if err != nil {
		return nil, fmt.Errorf("skillrepo: GetBranch: %w", err)
	}
	headSHA := ""
	if branch != nil && branch.Commit != nil && branch.Commit.SHA != nil {
		headSHA = *branch.Commit.SHA
	}
	fileSHA := ""
	if fileC.SHA != nil {
		fileSHA = *fileC.SHA
	}
	return &FileContents{Content: []byte(content), FileSHA: fileSHA, HeadSHA: headSHA}, nil
}

// CreateBranch creates a new ref pointing at fromSHA. Returns ErrConflict
// if the branch already exists.
func (c *Client) CreateBranch(ctx context.Context, installID int64, owner, repo, branch, fromSHA string) error {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return err
	}
	ref := &gh.Reference{
		Ref:    gh.Ptr("refs/heads/" + branch),
		Object: &gh.GitObject{SHA: gh.Ptr(fromSHA)},
	}
	_, resp, err := cli.Git.CreateRef(ctx, owner, repo, ref)
	if err != nil {
		if resp != nil && resp.StatusCode == 422 {
			return ErrConflict
		}
		return fmt.Errorf("skillrepo: CreateRef: %w", err)
	}
	return nil
}

// PutFile creates or updates a file on the named branch. fileSHA must
// match the file's current sha on the branch (use FileContents.FileSHA
// from GetFile). Returns ErrConflict on stale sha.
func (c *Client) PutFile(ctx context.Context, installID int64, owner, repo, branch, path, fileSHA, message string, content []byte) error {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return err
	}
	opts := &gh.RepositoryContentFileOptions{
		Message: gh.Ptr(message),
		Content: content,
		SHA:     gh.Ptr(fileSHA),
		Branch:  gh.Ptr(branch),
	}
	_, resp, err := cli.Repositories.UpdateFile(ctx, owner, repo, path, opts)
	if err != nil {
		if resp != nil && (resp.StatusCode == 409 || resp.StatusCode == 422) {
			return ErrConflict
		}
		return fmt.Errorf("skillrepo: UpdateFile: %w", err)
	}
	return nil
}

// CreatePR opens a PR. Returns ErrPermissionRequired if the App lacks
// pull_requests:write. Returns ErrConflict if a PR is already open for
// the same branch.
func (c *Client) CreatePR(ctx context.Context, installID int64, req PRRequest) (*PRResult, error) {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return nil, err
	}
	pr, resp, err := cli.PullRequests.Create(ctx, req.Owner, req.Repo, &gh.NewPullRequest{
		Title: gh.Ptr(req.Title),
		Body:  gh.Ptr(req.Body),
		Head:  gh.Ptr(req.Branch),
		Base:  gh.Ptr(req.Base),
	})
	if err != nil {
		if resp != nil {
			if resp.StatusCode == http.StatusForbidden {
				return nil, ErrPermissionRequired
			}
			if resp.StatusCode == http.StatusUnprocessableEntity {
				return nil, ErrConflict
			}
		}
		return nil, fmt.Errorf("skillrepo: CreatePR: %w", err)
	}
	return &PRResult{
		HTMLURL: pr.GetHTMLURL(),
		Number:  pr.GetNumber(),
	}, nil
}
