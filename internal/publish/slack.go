package publish

import (
	"context"
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

// NewSlackPublisher returns a Publisher that posts messages to Slack incoming
// webhooks. The webhook URL is resolved from the destination's Secret via the
// SecretGetter.
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
