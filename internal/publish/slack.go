package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const slackDefaultMaxChars = 35000

type slackPub struct {
	http *http.Client
}

// NewSlackPublisher returns a Publisher that posts to Slack incoming webhooks.
func NewSlackPublisher() Publisher {
	return &slackPub{http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *slackPub) Type() string { return "slack" }

func (p *slackPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Slack
	if d == nil || d.Secret == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("slack: secret required")}
	}
	url, err := secrets.Get(d.Secret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("slack: resolve secret: %w", err)}
	}
	text := output
	if d.Text != "" {
		text, _ = template.Render(d.Text, tctx)
	}
	text = ensureLen(text, slackDefaultMaxChars)
	return postJSON(ctx, p.http, p.Type(), url, map[string]any{"text": text})
}

// ensureLen returns s limited to maxRunes runes, appending "..." when truncated.
func ensureLen(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-3]) + "..."
}

// postJSON posts the payload as JSON with a small retry policy:
// 3 attempts, exponential backoff; 4xx responses are not retried.
func postJSON(ctx context.Context, c *http.Client, typ, url string, payload any) Result {
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: marshal: %w", typ, err)}
	}
	var lastErr error
	delays := []time.Duration{0, 1 * time.Second, 4 * time.Second}
	for _, d := range delays {
		if d > 0 {
			select {
			case <-ctx.Done():
				return Result{Type: typ, OK: false, Err: ctx.Err()}
			case <-time.After(d):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return Result{Type: typ, OK: true, Detail: fmt.Sprintf("http %d", resp.StatusCode)}
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: http %d (no retry)", typ, resp.StatusCode)}
		}
		lastErr = fmt.Errorf("http %d", resp.StatusCode)
	}
	return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: retries exhausted: %w", typ, lastErr)}
}
