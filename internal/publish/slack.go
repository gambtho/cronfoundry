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

	if d.Format == "text" {
		text = ensureLen(text, slackDefaultMaxChars)
		return postJSON(ctx, p.http, p.Type(), url, map[string]any{"text": text})
	}

	// Default: Block Kit
	header := tctx.Skill.Name
	if tctx.RunDate != "" {
		header += " · " + tctx.RunDate
	}
	var blocks []map[string]any
	if header != "" {
		blocks = append(blocks, map[string]any{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": ensureLen(header, 150)},
		})
	}
	const blockMax = 3000
	for len(text) > 0 {
		chunk := text
		if len(chunk) > blockMax {
			chunk = text[:blockMax]
			text = text[blockMax:]
		} else {
			text = ""
		}
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": chunk},
		})
	}
	return postJSON(ctx, p.http, p.Type(), url, map[string]any{"blocks": blocks})
}
