package publish

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const teamsDefaultMaxChars = 25000

type teamsPub struct {
	http *http.Client
}

// NewTeamsPublisher returns a Publisher that posts Adaptive Cards to a Teams
// webhook (configured via Power Automate's "When a Teams webhook request is
// received" trigger).
func NewTeamsPublisher() Publisher {
	return &teamsPub{http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *teamsPub) Type() string { return "teams" }

func (p *teamsPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Teams
	if d == nil || d.Secret == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("teams: secret required")}
	}
	url, err := secrets.Get(d.Secret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("teams: resolve secret: %w", err)}
	}
	text := output
	if d.Text != "" {
		text, _ = template.Render(d.Text, tctx)
	}
	text = ensureLen(text, teamsDefaultMaxChars)

	var body []map[string]any

	if d.Format == "card" {
		if d.Title != "" {
			body = append(body, map[string]any{
				"type":   "TextBlock",
				"text":   d.Title,
				"weight": "Bolder",
				"size":   "Medium",
			})
		}
		var facts []map[string]any
		if tctx.Skill.Name != "" {
			facts = append(facts, map[string]any{"title": "Skill", "value": tctx.Skill.Name})
		}
		if tctx.RunDate != "" {
			facts = append(facts, map[string]any{"title": "Date", "value": tctx.RunDate})
		}
		if tctx.RunID != "" {
			facts = append(facts, map[string]any{"title": "Run ID", "value": tctx.RunID})
		}
		if len(facts) > 0 {
			body = append(body, map[string]any{"type": "FactSet", "facts": facts})
		}
		body = append(body, map[string]any{"type": "TextBlock", "text": text, "wrap": true})
	} else {
		if d.Title != "" {
			body = append(body, map[string]any{
				"type":   "TextBlock",
				"text":   d.Title,
				"weight": "Bolder",
				"size":   "Medium",
			})
		}
		body = append(body, map[string]any{"type": "TextBlock", "text": text, "wrap": true})
	}

	card := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"type":    "AdaptiveCard",
				"version": "1.4",
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"body":    body,
			},
		}},
	}
	return postJSON(ctx, p.http, p.Type(), url, card)
}
