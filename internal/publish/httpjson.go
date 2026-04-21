package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"
)

// Shared HTTP/JSON helpers used by the webhook-style publishers
// (Slack, Discord, Teams). Kept unexported: these are package-internal
// building blocks, not part of the Publisher interface.

// ensureLen returns s limited to maxRunes runes, appending "..." when truncated.
// The marker itself counts toward maxRunes, so the returned string never
// exceeds the cap (measured in runes, not bytes).
func ensureLen(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-3]) + "..."
}

// postJSON marshals payload as JSON and POSTs it to url with a small retry
// policy: 3 attempts at 0s / 1s / 4s, respecting ctx cancellation between
// attempts. 2xx responses are treated as success; 4xx responses are returned
// immediately without retry (client errors won't fix themselves). 5xx and
// transport errors are retried. On exhaustion the last error is wrapped with
// the publisher's type prefix.
func postJSON(ctx context.Context, c *http.Client, typ, url string, payload any) Result {
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: marshal: %w", typ, err)}
	}
	var lastErr error
	delays := []time.Duration{0, 1 * time.Second, 4 * time.Second}
	for _, d := range delays {
		if d > 0 {
			// Add ±25% jitter so simultaneous runs don't retry in lockstep
			// (small thundering-herd mitigation on downstream webhooks).
			jitter := time.Duration(rand.Int64N(int64(d)/2)) - d/4
			d += jitter
		}
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
		_ = resp.Body.Close()
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
