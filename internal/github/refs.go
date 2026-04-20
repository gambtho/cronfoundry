package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetBranchHead returns the commit SHA at the tip of the named branch.
// installToken is the short-lived token minted via InstallationCache.
func GetBranchHead(
	ctx context.Context,
	client *http.Client,
	baseURL, installToken, owner, name, branch string,
) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/branches/%s", baseURL, owner, name, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: GetBranchHead: new request: %w", err)
	}
	req.Header.Set("Authorization", "token "+installToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: GetBranchHead: http do: %w", err)
	}
	defer resp.Body.Close()

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
