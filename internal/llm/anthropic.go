package llm

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	baseURL string
}

// NewAnthropic returns a Provider backed by github.com/anthropics/anthropic-sdk-go.
// When baseURL is empty, the SDK's default (api.anthropic.com) is used.
func NewAnthropic(baseURL string) Provider {
	return &anthropicProvider{baseURL: baseURL}
}

func (p *anthropicProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	clientOpts := []option.RequestOption{option.WithAPIKey(opts.APIKey)}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)

	// Anthropic takes the system prompt as a top-level parameter; user
	// messages are supplied via the Messages slice. Multiple system messages
	// are concatenated with a blank line between them.
	var system string
	var userMsgs []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case RoleUser:
			userMsgs = append(userMsgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	maxTok := int64(opts.MaxTokens)
	if maxTok <= 0 {
		maxTok = 1024
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(opts.Model),
		MaxTokens: maxTok,
		Messages:  userMsgs,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var usage Usage
	for stream.Next() {
		evt := stream.Current()
		switch v := evt.AsAny().(type) {
		case anthropic.MessageStartEvent:
			usage.InputTokens = int(v.Message.Usage.InputTokens)
		case anthropic.ContentBlockDeltaEvent:
			if td, ok := v.Delta.AsAny().(anthropic.TextDelta); ok {
				onChunk(StreamChunk{Delta: td.Text})
			}
		case anthropic.MessageDeltaEvent:
			if v.Usage.OutputTokens > 0 {
				usage.OutputTokens = int(v.Usage.OutputTokens)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return usage, fmt.Errorf("anthropic chat: %w", err)
	}
	return usage, nil
}
