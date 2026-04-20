package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// GetBranchHead returns the commit SHA at the tip of the named branch.
// installToken is the short-lived token minted via InstallationCache.
//
// Path segments (owner/name/branch) are URL-escaped so branches containing
// `/` (e.g. `feat/my-work`) don't get mis-parsed as additional path
// segments by GitHub.
func GetBranchHead(
	ctx context.Context,
	client *http.Client,
	baseURL, installToken, owner, name, branch string,
) (string, error) {
	reqURL := fmt.Sprintf("%s/repos/%s/%s/branches/%s",
		baseURL,
		url.PathEscape(owner),
		url.PathEscape(name),
		url.PathEscape(branch),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("github: GetBranchHead: new request: %w", err)
	}
	req.Header.Set("Authorization", "token "+installToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: GetBranchHead: http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github: GetBranchHead: http %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("github: GetBranchHead: decode: %w", err)
	}
	if payload.Commit.SHA == "" {
		return "", fmt.Errorf("github: GetBranchHead: empty sha in response")
	}
	return payload.Commit.SHA, nil
}
