package publish

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const discordHardLimit = 2000

type discordPub struct {
	http *http.Client
}

// NewDiscordPublisher returns a Publisher that posts messages to Discord
// incoming webhooks. The webhook URL is resolved from the destination's
// Secret via the SecretGetter.
func NewDiscordPublisher() Publisher {
	return &discordPub{http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *discordPub) Type() string { return "discord" }

func (p *discordPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Discord
	if d == nil || d.Secret == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("discord: secret required")}
	}
	url, err := secrets.Get(d.Secret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("discord: resolve secret: %w", err)}
	}
	content := output
	if d.Content != "" {
		content, _ = template.Render(d.Content, tctx)
	}
	content = ensureLen(content, discordHardLimit)
	payload := map[string]any{"content": content}
	if d.Username != "" {
		payload["username"] = d.Username
	}
	return postJSON(ctx, p.http, p.Type(), url, payload)
}
