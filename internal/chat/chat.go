// Package chat implements the in-app assistant: a stateless tool-using agent
// loop that helps operators set up jobs and troubleshoot failures.
//
// The package owns three responsibilities:
//
//  1. Building the system prompt (assistant persona + embedded doc grounding).
//  2. Running an N-turn ChatTurn loop against any llm.ToolCapableProvider.
//  3. Routing tool calls to read-only inspection helpers that wrap the same
//     queries the rest of webapi uses (runs, schedules, skills, audit).
//
// The agent is intentionally read-only. Setup help is delivered as YAML
// snippets the operator pastes into their repo — there is no code path that
// mutates schedules, secrets, or repos from chat.
package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gambtho/cronfoundry/internal/llm"
)

// Config holds per-request configuration for a chat turn.
type Config struct {
	// Provider is the LLM provider implementation. Must support tool use.
	Provider llm.ToolCapableProvider
	// Model is the provider-specific model identifier.
	Model string
	// APIKey authenticates the LLM call.
	APIKey string
	// Endpoint / Deployment are forwarded to llm.CallOptions for Azure.
	Endpoint   string
	Deployment string
	// MaxTurns caps the tool-loop iteration count. Falls back to 6.
	MaxTurns int
	// MaxTokens caps each turn's output. Falls back to 2048.
	MaxTokens int
	// Tools is the read-only tool surface available to the model.
	Tools Toolbox
	// Caller carries identity (login, role) for tool authorization decisions.
	Caller Caller
	// PageContext is an optional short string describing where the user is in
	// the UI ("/jobs/abc123"). It's included in the system prompt verbatim.
	PageContext string
}

// Caller identifies the human asking the chat agent for help. Tools may use
// the role to gate behavior — e.g. an admin can see manifest contents that
// reference secret names, while a viewer only sees redacted summaries.
type Caller struct {
	Login string
	Role  string // "admin" | "viewer"
}

// Message is a chat turn for the agent loop. Mirrors llm.Message but with a
// JSON-friendly shape suitable for the SSE wire protocol.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// Sink receives streaming events as the agent runs. Implementations are
// expected to be cheap and non-blocking; the SSE handler buffers writes via
// http.Flusher.
type Sink interface {
	// Token is called for every text delta the model emits.
	Token(delta string)
	// ToolStart is called when a tool call begins (for UI signaling).
	ToolStart(name string, input json.RawMessage)
	// ToolEnd is called when a tool call completes. errMsg is empty on success.
	ToolEnd(name string, errMsg string)
}

// noopSink is the fallback Sink used when callers pass nil. It silently drops
// every event so Run can stream unconditionally without nil-checking each
// callback site.
type noopSink struct{}

func (noopSink) Token(string)                      {}
func (noopSink) ToolStart(string, json.RawMessage) {}
func (noopSink) ToolEnd(string, string)            {}

// Run executes one user message against the agent loop, streaming output
// through sink. The history slice contains prior assistant/user turns; the
// caller is responsible for appending the new user message before calling
// Run. Run returns the final assistant text plus aggregated usage.
func Run(ctx context.Context, cfg Config, history []Message, sink Sink) (string, llm.Usage, error) {
	if cfg.Provider == nil {
		return "", llm.Usage{}, fmt.Errorf("chat: provider is nil")
	}
	if sink == nil {
		sink = noopSink{}
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 6
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	tools := cfg.Tools.Defs()
	systemPrompt := buildSystemPrompt(cfg.Caller, cfg.PageContext)

	msgs := []llm.Message{{Role: llm.RoleSystem, Content: systemPrompt}}
	for _, m := range history {
		switch m.Role {
		case "user":
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: m.Content})
		case "assistant":
			msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: m.Content})
		}
	}

	var totalUsage llm.Usage
	var finalText string

	for turn := 0; turn < maxTurns; turn++ {
		tr, err := cfg.Provider.ChatTurn(ctx, msgs, tools, llm.CallOptions{
			Model:      cfg.Model,
			MaxTokens:  maxTokens,
			APIKey:     cfg.APIKey,
			Endpoint:   cfg.Endpoint,
			Deployment: cfg.Deployment,
		}, func(c llm.StreamChunk) {
			if c.Delta != "" {
				sink.Token(c.Delta)
			}
		})
		if err != nil {
			return "", totalUsage, fmt.Errorf("chat: turn %d: %w", turn+1, err)
		}
		totalUsage.InputTokens += tr.Usage.InputTokens
		totalUsage.OutputTokens += tr.Usage.OutputTokens

		// Record the assistant turn (text + any tool_use blocks) so the
		// model sees its own prior output on the next iteration.
		msgs = append(msgs, llm.Message{
			Role:     llm.RoleAssistant,
			Content:  tr.Text,
			ToolUses: tr.ToolUses,
		})

		if len(tr.ToolUses) == 0 {
			finalText = tr.Text
			break
		}

		// Dispatch each tool call serially. Errors are reported back to the
		// model as a tool_result with is_error semantics — the model may
		// recover by trying a different tool or asking the user.
		for _, tu := range tr.ToolUses {
			sink.ToolStart(tu.Name, tu.Input)
			result, err := cfg.Tools.Call(ctx, cfg.Caller, tu.Name, tu.Input)
			if err != nil {
				sink.ToolEnd(tu.Name, err.Error())
				msgs = append(msgs, llm.Message{
					Role:      llm.RoleTool,
					ToolUseID: tu.ID,
					Content:   fmt.Sprintf(`{"error":%q}`, err.Error()),
				})
				continue
			}
			sink.ToolEnd(tu.Name, "")
			msgs = append(msgs, llm.Message{
				Role:      llm.RoleTool,
				ToolUseID: tu.ID,
				Content:   string(result),
			})
		}
	}

	if finalText == "" {
		finalText = "I ran out of turns before reaching an answer. Try rephrasing or breaking the request into smaller steps."
	}
	return finalText, totalUsage, nil
}
