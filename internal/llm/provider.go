// Package llm defines a provider-agnostic interface for streaming chat
// completions against OpenAI, Anthropic, and Azure AI Foundry.
package llm

import (
	"context"
	"encoding/json"
)

// Role is the conversational role of a Message.
type Role string

// Conversational roles supported by the Provider interface. The chat
// protocol currently distinguishes only system (instructions) and user
// (prompt) messages; assistant replies are streamed back as StreamChunks.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single chat message.
type Message struct {
	Role      Role
	Content   string
	ToolUses  []ToolUse // RoleAssistant: tool_use blocks the model emitted
	ToolUseID string    // RoleTool: id of the call this message answers
}

// StreamChunk is an incremental portion of the assistant's response.
type StreamChunk struct {
	Delta string
}

// Usage records token accounting for a completed call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// CallOptions controls a single provider invocation.
type CallOptions struct {
	Model     string
	MaxTokens int
	APIKey    string
	// Endpoint is used by Azure AI Foundry only; OpenAI and Anthropic ignore it.
	Endpoint string
	// Deployment (Azure AI Foundry): the model-deployment name.
	Deployment string
}

// Provider executes a single chat completion with streaming output.
//
// Implementations MUST:
//   - stream chunks to onChunk as they arrive (empty deltas are skipped),
//   - return Usage with final input/output token counts when reported by
//     the upstream API; zero values are returned if the provider does not
//     emit a usage record,
//   - honor ctx cancellation / deadline,
//   - return an error wrapping the underlying SDK error, prefixed with
//     the provider name (e.g. "openai chat: ..."), when the streamed call
//     ultimately fails.
type Provider interface {
	Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error)
}

// ToolUse represents a tool invocation emitted by the model.
type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolDef describes a tool available to the model.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// TurnResult is one tool-aware turn's output.
type TurnResult struct {
	Text       string
	ToolUses   []ToolUse
	Usage      Usage
	StopReason string // "end_turn" | "tool_use" | "max_tokens" | "stop"
}

// ToolCapableProvider supports tool-aware completion turns.
type ToolCapableProvider interface {
	Provider
	ChatTurn(
		ctx context.Context,
		messages []Message,
		tools []ToolDef,
		opts CallOptions,
		onChunk func(StreamChunk),
	) (TurnResult, error)
}
