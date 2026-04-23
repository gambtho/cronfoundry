package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

type openAIProvider struct {
	baseURL string
}

// NewOpenAI returns a Provider backed by github.com/openai/openai-go.
// When baseURL is empty, the SDK's default (api.openai.com) is used.
func NewOpenAI(baseURL string) Provider {
	return &openAIProvider{baseURL: baseURL}
}

func (p *openAIProvider) newClient(apiKey string) *openai.Client {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(3),
	}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	c := openai.NewClient(opts...)
	return &c
}

func (p *openAIProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	client := p.newClient(opts.APIKey)

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
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(opts.MaxTokens))
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

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

func (p *openAIProvider) ChatTurn(
	ctx context.Context,
	messages []Message,
	tools []ToolDef,
	opts CallOptions,
	onChunk func(StreamChunk),
) (TurnResult, error) {
	client := p.newClient(opts.APIKey)

	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case RoleUser:
			msgs = append(msgs, openai.UserMessage(m.Content))
		case RoleAssistant:
			am := openai.AssistantMessage(m.Content)
			if len(m.ToolUses) > 0 {
				tcs := make([]openai.ChatCompletionMessageToolCallParam, len(m.ToolUses))
				for i, tu := range m.ToolUses {
					tcs[i] = openai.ChatCompletionMessageToolCallParam{
						ID: tu.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tu.Name,
							Arguments: string(tu.Input),
						},
					}
				}
				am.OfAssistant.ToolCalls = tcs
			}
			msgs = append(msgs, am)
		case RoleTool:
			msgs = append(msgs, openai.ToolMessage(m.Content, m.ToolUseID))
		}
	}

	var apiTools []openai.ChatCompletionToolParam
	for _, t := range tools {
		var params shared.FunctionParameters
		inputSchema := t.InputSchema
		if len(inputSchema) == 0 {
			inputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if err := json.Unmarshal(inputSchema, &params); err != nil {
			return TurnResult{}, fmt.Errorf("openai: unmarshal tool schema %q: %w", t.Name, err)
		}
		apiTools = append(apiTools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  params,
			},
		})
	}

	streamParams := openai.ChatCompletionNewParams{
		Model:    opts.Model,
		Messages: msgs,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if opts.MaxTokens > 0 {
		streamParams.MaxTokens = openai.Int(int64(opts.MaxTokens))
	}
	if len(apiTools) > 0 {
		streamParams.Tools = apiTools
	}

	stream := client.Chat.Completions.NewStreaming(ctx, streamParams)
	defer func() { _ = stream.Close() }()

	var result TurnResult
	type toolAccum struct {
		id   string
		name string
		args []byte
	}
	toolsByIndex := map[int64]*toolAccum{}
	var textBuf string

	for stream.Next() {
		evt := stream.Current()
		if len(evt.Choices) > 0 {
			choice := evt.Choices[0]
			if choice.Delta.Content != "" {
				textBuf += choice.Delta.Content
				onChunk(StreamChunk{Delta: choice.Delta.Content})
			}
			for _, tc := range choice.Delta.ToolCalls {
				acc, ok := toolsByIndex[tc.Index]
				if !ok {
					acc = &toolAccum{}
					toolsByIndex[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args = append(acc.args, tc.Function.Arguments...)
			}
			if choice.FinishReason != "" {
				result.StopReason = choice.FinishReason
			}
		}
		if evt.Usage.PromptTokens > 0 || evt.Usage.CompletionTokens > 0 {
			result.Usage.InputTokens = int(evt.Usage.PromptTokens)
			result.Usage.OutputTokens = int(evt.Usage.CompletionTokens)
		}
	}
	if err := stream.Err(); err != nil {
		return result, fmt.Errorf("openai chat_turn: %w", err)
	}

	for i := int64(0); ; i++ {
		acc, ok := toolsByIndex[i]
		if !ok {
			break
		}
		result.ToolUses = append(result.ToolUses, ToolUse{
			ID:    acc.id,
			Name:  acc.name,
			Input: json.RawMessage(acc.args),
		})
	}
	result.Text = textBuf
	return result, nil
}
