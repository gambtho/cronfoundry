package llm

import (
	"context"
	"encoding/json"
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

func (p *anthropicProvider) newClient(apiKey string) *anthropic.Client {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(3),
	}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	c := anthropic.NewClient(opts...)
	return &c
}

func (p *anthropicProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	client := p.newClient(opts.APIKey)

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
	defer func() { _ = stream.Close() }()

	var usage Usage
	for stream.Next() {
		evt := stream.Current()
		switch v := evt.AsAny().(type) {
		case anthropic.MessageStartEvent:
			if v.Message.Usage.InputTokens > 0 {
				usage.InputTokens = int(v.Message.Usage.InputTokens)
			}
		case anthropic.ContentBlockDeltaEvent:
			if td, ok := v.Delta.AsAny().(anthropic.TextDelta); ok && td.Text != "" {
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

func (p *anthropicProvider) ChatTurn(
	ctx context.Context,
	messages []Message,
	tools []ToolDef,
	opts CallOptions,
	onChunk func(StreamChunk),
) (TurnResult, error) {
	client := p.newClient(opts.APIKey)

	var system string
	var apiMsgs []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case RoleUser:
			apiMsgs = append(apiMsgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tu := range m.ToolUses {
				blocks = append(blocks, anthropic.NewToolUseBlock(tu.ID, tu.Input, tu.Name))
			}
			apiMsgs = append(apiMsgs, anthropic.NewAssistantMessage(blocks...))
		case RoleTool:
			apiMsgs = append(apiMsgs, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolUseID, m.Content, false),
			))
		}
	}

	var apiTools []anthropic.ToolUnionParam
	for _, t := range tools {
		var schema anthropic.ToolInputSchemaParam
		inputSchema := t.InputSchema
		if len(inputSchema) == 0 {
			inputSchema = json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
		}
		if err := json.Unmarshal(inputSchema, &schema); err != nil {
			return TurnResult{}, fmt.Errorf("anthropic: unmarshal tool schema %q: %w", t.Name, err)
		}
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: schema,
			},
		})
	}

	maxTok := int64(opts.MaxTokens)
	if maxTok <= 0 {
		maxTok = 4096
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(opts.Model),
		MaxTokens: maxTok,
		Messages:  apiMsgs,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var result TurnResult
	var textBuf string
	var curToolID, curToolName string
	var curToolInputBuf []byte

	flushToolUse := func() {
		if curToolID == "" {
			return
		}
		result.ToolUses = append(result.ToolUses, ToolUse{
			ID: curToolID, Name: curToolName, Input: json.RawMessage(curToolInputBuf),
		})
		curToolID, curToolName, curToolInputBuf = "", "", nil
	}

	for stream.Next() {
		evt := stream.Current()
		switch v := evt.AsAny().(type) {
		case anthropic.MessageStartEvent:
			if v.Message.Usage.InputTokens > 0 {
				result.Usage.InputTokens = int(v.Message.Usage.InputTokens)
			}
		case anthropic.ContentBlockStartEvent:
			flushToolUse()
			if tu, ok := v.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
				curToolID = tu.ID
				curToolName = tu.Name
				curToolInputBuf = []byte{}
			}
		case anthropic.ContentBlockDeltaEvent:
			switch d := v.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if d.Text != "" {
					textBuf += d.Text
					onChunk(StreamChunk{Delta: d.Text})
				}
			case anthropic.InputJSONDelta:
				curToolInputBuf = append(curToolInputBuf, d.PartialJSON...)
			}
		case anthropic.ContentBlockStopEvent:
			flushToolUse()
		case anthropic.MessageDeltaEvent:
			if v.Usage.OutputTokens > 0 {
				result.Usage.OutputTokens = int(v.Usage.OutputTokens)
			}
			if v.Delta.StopReason != "" {
				result.StopReason = string(v.Delta.StopReason)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return result, fmt.Errorf("anthropic chat_turn: %w", err)
	}
	flushToolUse()
	result.Text = textBuf
	return result, nil
}
