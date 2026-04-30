// internal/bootstrap/azure/image.go
package azure

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const ghcrRoot = "https://ghcr.io"

// ProbeImage checks ghcr.io/<owner>/cronfoundry:<tag> exists anonymously.
// On 404 it returns an error suggesting the operator push a v* tag.
func ProbeImage(ctx context.Context, owner, tag string) error {
	return probeImageAt(ctx, ghcrRoot, owner, tag)
}

func probeImageAt(ctx context.Context, root, owner, tag string) error {
	url := fmt.Sprintf("%s/v2/%s/cronfoundry/manifests/%s",
		strings.TrimRight(root, "/"), owner, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf(
			"image %s not found. Publish it first:\n  git tag v%s && git push origin v%s\n(Wait ~5 min for the Release workflow.)",
			url, tag, tag)
	default:
		return fmt.Errorf("probe %s: HTTP %d", url, resp.StatusCode)
	}
}
