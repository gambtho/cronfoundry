package publish

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-github/v74/github"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

// tokenAuthTransport injects `Authorization: token <pat>` on each request.
// We do this by hand (rather than Client.WithAuthToken) because go-github
// v74 uses `Bearer` on that helper, and our spec requires the classic
// `token` prefix compatible with Personal Access Tokens.
type tokenAuthTransport struct {
	token string
	base  http.RoundTripper
}

func (t *tokenAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone per net/http convention to avoid mutating the caller's request.
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "token "+t.token)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

const issueBodyMaxBytes = 64 * 1024 // GitHub issue body ~64KB

type githubIssuePub struct {
	baseURL       string
	fallbackToken string
}

// NewGitHubIssuePublisher constructs the publisher. The token is looked up
// via SecretGetter using the reserved name "github_token"; if unavailable,
// fallbackToken is used (P1: passed through env; P2 will swap for a GitHub
// App installation token).
func NewGitHubIssuePublisher(baseURL, fallbackToken string) Publisher {
	return &githubIssuePub{baseURL: baseURL, fallbackToken: fallbackToken}
}

func (p *githubIssuePub) Type() string { return "github-issue" }

func (p *githubIssuePub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.GitHubIssue
	if d == nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: config missing")}
	}
	owner, repo, ok := splitRepo(d.Repo)
	if !ok {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: repo must be owner/name (got %q)", d.Repo)}
	}

	token, err := secrets.Get("github_token")
	if err != nil || token == "" {
		token = p.fallbackToken
	}
	if token == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: no GitHub token available (set GITHUB_TOKEN)")}
	}

	httpClient := &http.Client{Transport: &tokenAuthTransport{token: token}}
	client := github.NewClient(httpClient)
	if p.baseURL != "" {
		base := p.baseURL
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		parsed, err := url.Parse(base)
		if err != nil {
			return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: parse base: %w", err)}
		}
		client.BaseURL = parsed
	}

	title, _ := template.Render(d.Title, tctx)
	body := output
	if d.Body != "" {
		body, _ = template.Render(d.Body, tctx)
	}
	if len(body) > issueBodyMaxBytes {
		// Cut at a byte boundary then strip any trailing invalid UTF-8
		// fragment so a multibyte rune straddling the cut point doesn't
		// render as a replacement character (U+FFFD) on GitHub.
		truncated := strings.ToValidUTF8(body[:issueBodyMaxBytes-64], "")
		body = truncated + "\n\n... [truncated, full output in run log]"
	}

	req := &github.IssueRequest{
		Title:     &title,
		Body:      &body,
		Labels:    &d.Labels,
		Assignees: &d.Assignees,
	}
	issue, _, err := client.Issues.Create(ctx, owner, repo, req)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: create: %w", err)}
	}
	return Result{Type: p.Type(), OK: true, Detail: issue.GetHTMLURL()}
}

// splitRepo parses "owner/name" into its parts. Empty owner, empty name, or
// missing slash all fail.
func splitRepo(r string) (string, string, bool) {
	idx := strings.Index(r, "/")
	if idx <= 0 || idx == len(r)-1 {
		return "", "", false
	}
	return r[:idx], r[idx+1:], true
}
