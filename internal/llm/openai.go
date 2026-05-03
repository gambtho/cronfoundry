package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

type openAIProvider struct {
	baseURL   string
	extraOpts []option.RequestOption
}

// NewOpenAI returns a Provider backed by github.com/openai/openai-go.
// When baseURL is empty, the SDK's default (api.openai.com) is used.
func NewOpenAI(baseURL string) Provider {
	return &openAIProvider{baseURL: baseURL}
}

// chatErr unwraps a streaming error so the response body is visible in logs.
// The openai-go SDK's *openai.Error.Error() includes JSON.raw, but only when
// the response body is parseable JSON; non-JSON 400s (which Copilot tends to
// return for bad models or missing headers) lose the body in the wrapping.
// Pull StatusCode + RawJSON explicitly so 'POST .../chat/completions: 400
// Bad Request' becomes 'POST .../chat/completions: 400 Bad Request: model
// "gpt-4o" is not supported'.
func chatErr(prefix string, err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		body := apiErr.RawJSON()
		if body == "" && apiErr.Response != nil {
			// Non-JSON body (Copilot/other gateways occasionally return
			// text/html for misconfigured requests). DumpResponse(true)
			// reads the buffered response body the SDK retained on the
			// error; safe to call without closing — the SDK has already
			// drained and rewound it.
			if dump := apiErr.DumpResponse(true); len(dump) > 0 {
				body = string(dump)
			}
		}
		if body == "" {
			body = "(empty body)"
		}
		return fmt.Errorf("%s: status=%d body=%s: %w",
			prefix, apiErr.StatusCode, body, err)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func (p *openAIProvider) newClient(apiKey string) *openai.Client {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(3),
	}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	opts = append(opts, p.extraOpts...)
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
		return usage, chatErr("openai chat", err)
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
		return result, chatErr("openai chat_turn", err)
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
