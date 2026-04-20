package llm

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type openAIProvider struct {
	baseURL string
}

// NewOpenAI returns a Provider backed by github.com/openai/openai-go.
// When baseURL is empty, the SDK's default (api.openai.com) is used.
func NewOpenAI(baseURL string) Provider {
	return &openAIProvider{baseURL: baseURL}
}

func (p *openAIProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	clientOpts := []option.RequestOption{option.WithAPIKey(opts.APIKey)}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := openai.NewClient(clientOpts...)

	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case RoleUser:
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    opts.Model,
		Messages: msgs,
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(opts.MaxTokens))
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var usage Usage
	for stream.Next() {
		evt := stream.Current()
		if len(evt.Choices) > 0 && evt.Choices[0].Delta.Content != "" {
			onChunk(StreamChunk{Delta: evt.Choices[0].Delta.Content})
		}
		if evt.Usage.PromptTokens > 0 || evt.Usage.CompletionTokens > 0 {
			usage.InputTokens = int(evt.Usage.PromptTokens)
			usage.OutputTokens = int(evt.Usage.CompletionTokens)
		}
	}
	if err := stream.Err(); err != nil {
		return usage, fmt.Errorf("openai chat: %w", err)
	}
	return usage, nil
}
