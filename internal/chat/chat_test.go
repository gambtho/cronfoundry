package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gambtho/cronfoundry/internal/llm"
)

// fakeProvider drives the agent loop with a scripted sequence of TurnResults
// so we can assert the loop's tool-routing and termination behavior without
// hitting a real LLM.
type fakeProvider struct {
	turns   []llm.TurnResult
	calls   []callRecord
	chunks  [][]string // optional per-turn token deltas to stream
	wantErr bool
}

type callRecord struct {
	model    string
	apiKey   string
	maxTok   int
	messages []llm.Message
	tools    []llm.ToolDef
}

func (f *fakeProvider) Chat(ctx context.Context, messages []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	return llm.Usage{}, nil
}

func (f *fakeProvider) ChatTurn(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.TurnResult, error) {
	idx := len(f.calls)
	f.calls = append(f.calls, callRecord{
		model:    opts.Model,
		apiKey:   opts.APIKey,
		maxTok:   opts.MaxTokens,
		messages: append([]llm.Message(nil), messages...),
		tools:    tools,
	})
	if f.wantErr {
		return llm.TurnResult{}, errors.New("fake provider error")
	}
	if idx >= len(f.turns) {
		return llm.TurnResult{Text: "fallback"}, nil
	}
	if idx < len(f.chunks) {
		for _, s := range f.chunks[idx] {
			onChunk(llm.StreamChunk{Delta: s})
		}
	}
	return f.turns[idx], nil
}

// recordingSink captures Sink callbacks so tests can assert what the SSE
// layer would have streamed.
type recordingSink struct {
	tokens     []string
	toolStarts []string
	toolEnds   []string
}

func (s *recordingSink) Token(d string) { s.tokens = append(s.tokens, d) }
func (s *recordingSink) ToolStart(name string, _ json.RawMessage) {
	s.toolStarts = append(s.toolStarts, name)
}
func (s *recordingSink) ToolEnd(name string, errMsg string) {
	if errMsg == "" {
		s.toolEnds = append(s.toolEnds, name)
	} else {
		s.toolEnds = append(s.toolEnds, name+":err")
	}
}

// Note: Run takes a concrete Toolbox value, so unit tests of the loop drive
// multi-turn behavior using the real Toolbox{} and tool names that fall
// through Toolbox.Call's "unknown tool" default branch — no DB, no panic.
// See TestRun_MaxTurnsCap.

func TestRun_TerminatesOnTextOnlyTurn(t *testing.T) {
	prov := &fakeProvider{
		turns: []llm.TurnResult{
			{Text: "Hello, operator.", Usage: llm.Usage{InputTokens: 50, OutputTokens: 5}},
		},
		chunks: [][]string{{"Hello, ", "operator."}},
	}
	sink := &recordingSink{}
	cfg := Config{
		Provider:  prov,
		Model:     "test-model",
		APIKey:    "sk-test",
		MaxTokens: 100,
		Tools:     Toolbox{}, // no DB, no tool calls expected
		Caller:    Caller{Login: "alice", Role: "viewer"},
	}
	history := []Message{{Role: "user", Content: "hi"}}

	final, usage, err := Run(context.Background(), cfg, history, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if final != "Hello, operator." {
		t.Errorf("final = %q, want %q", final, "Hello, operator.")
	}
	if usage.InputTokens != 50 || usage.OutputTokens != 5 {
		t.Errorf("usage = %+v, want {50 5}", usage)
	}
	if got := strings.Join(sink.tokens, ""); got != "Hello, operator." {
		t.Errorf("streamed tokens = %q, want %q", got, "Hello, operator.")
	}
	if len(prov.calls) != 1 {
		t.Errorf("expected 1 ChatTurn call, got %d", len(prov.calls))
	}
	// Verify system prompt mentions the operator and is the first message.
	if len(prov.calls[0].messages) < 2 || prov.calls[0].messages[0].Role != llm.RoleSystem {
		t.Fatalf("first message must be system, got %+v", prov.calls[0].messages)
	}
	if !strings.Contains(prov.calls[0].messages[0].Content, "alice") {
		t.Errorf("system prompt should mention operator login; got: %s", prov.calls[0].messages[0].Content)
	}
}

func TestRun_PropagatesProviderError(t *testing.T) {
	prov := &fakeProvider{wantErr: true}
	sink := &recordingSink{}
	_, _, err := Run(context.Background(), Config{
		Provider: prov,
		Model:    "m",
		Tools:    Toolbox{},
	}, []Message{{Role: "user", Content: "hi"}}, sink)
	if err == nil {
		t.Fatal("expected error from provider; got nil")
	}
	if !strings.Contains(err.Error(), "fake provider error") {
		t.Errorf("error should wrap provider error, got: %v", err)
	}
}

func TestRun_RejectsNilProvider(t *testing.T) {
	_, _, err := Run(context.Background(), Config{}, []Message{{Role: "user", Content: "hi"}}, &recordingSink{})
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestRun_PageContextInSystemPrompt(t *testing.T) {
	prov := &fakeProvider{
		turns: []llm.TurnResult{{Text: "ok"}},
	}
	cfg := Config{
		Provider:    prov,
		Model:       "m",
		Tools:       Toolbox{},
		Caller:      Caller{Login: "bob", Role: "admin"},
		PageContext: "/jobs/abc-123",
	}
	if _, _, err := Run(context.Background(), cfg, []Message{{Role: "user", Content: "?"}}, &recordingSink{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sys := prov.calls[0].messages[0].Content
	if !strings.Contains(sys, "/jobs/abc-123") {
		t.Errorf("system prompt should include page context; got: %s", sys)
	}
}

func TestRun_MaxTurnsCap(t *testing.T) {
	// Every turn returns a tool_use targeting an unknown tool. Toolbox.Call's
	// default branch returns ("unknown tool") as a non-fatal error, which the
	// loop reports as a tool_result with is_error semantics — letting us drive
	// many turns without a DB or a panic. We script far more turns than
	// MaxTurns so the only way Run can exit cleanly is via the cap.
	const maxTurns = 3
	prov := &fakeProvider{}
	for range maxTurns + 5 {
		prov.turns = append(prov.turns, llm.TurnResult{
			ToolUses: []llm.ToolUse{{ID: "t1", Name: "definitely_not_a_real_tool", Input: json.RawMessage(`{}`)}},
		})
	}
	cfg := Config{
		Provider: prov,
		Model:    "m",
		MaxTurns: maxTurns,
		Tools:    Toolbox{},
	}
	sink := &recordingSink{}
	final, _, err := Run(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(final, "ran out of turns") {
		t.Errorf("expected 'ran out of turns' fallback, got: %q", final)
	}
	if got := len(prov.calls); got != maxTurns {
		t.Errorf("ChatTurn called %d times, want exactly MaxTurns=%d", got, maxTurns)
	}
	// Every turn should have surfaced a tool error to the sink.
	if len(sink.toolEnds) != maxTurns {
		t.Errorf("toolEnds = %d, want %d", len(sink.toolEnds), maxTurns)
	}
	for _, te := range sink.toolEnds {
		if !strings.HasSuffix(te, ":err") {
			t.Errorf("expected every tool dispatch to surface an error, got: %v", sink.toolEnds)
			break
		}
	}
}

func TestRun_NilSinkDoesNotPanic(t *testing.T) {
	prov := &fakeProvider{
		turns: []llm.TurnResult{{Text: "ok", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
	}
	final, _, err := Run(context.Background(), Config{
		Provider: prov,
		Model:    "m",
		Tools:    Toolbox{},
	}, []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Run with nil sink: %v", err)
	}
	if final != "ok" {
		t.Errorf("final = %q, want %q", final, "ok")
	}
}

func TestBuildSystemPrompt_IncludesDocs(t *testing.T) {
	got := buildSystemPrompt(Caller{Login: "x", Role: "viewer"}, "")
	// The corpus directory ships at least the concepts doc; verify it
	// landed in the prompt so we know the embed path is wired correctly.
	if !strings.Contains(got, "Core concepts") {
		t.Errorf("system prompt missing docs corpus; got: %s", got)
	}
	if !strings.Contains(got, "in-app assistant") {
		t.Errorf("system prompt missing persona language")
	}
}
