package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openAIStreamFixture is a minimal SSE stream matching the Chat Completions
// response format used by the openai-go SDK.
const openAIStreamFixture = `data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}

data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}

data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}

data: [DONE]
`

func TestOpenAI_Chat_StreamsAndReportsUsage(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIStreamFixture)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL)
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "user"}},
		CallOptions{Model: "gpt-5.1", MaxTokens: 128, APIKey: "sk-test"},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Contains(t, gotBody, `"model":"gpt-5.1"`)
	assert.Contains(t, gotBody, `"stream":true`)
	assert.Contains(t, gotBody, `"include_usage":true`)
	assert.Equal(t, []string{"Hello", " world"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}

func TestOpenAI_Chat_ErrorOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "u"}},
		CallOptions{Model: "m", APIKey: "k"},
		func(StreamChunk) {})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "boom"))
}

// chatErr surfaces the response body in the error message so 'unsupported
// model' / 'missing field' / etc. are visible to operators. Without this
// the runner-side log just says 'POST .../chat/completions: 400 Bad
// Request' with no clue why.
func TestOpenAI_Chat_400ErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model 'gpt-4o' is not supported","code":"model_not_found"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "u"}},
		CallOptions{Model: "gpt-4o", APIKey: "k"},
		func(StreamChunk) {})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "openai chat", "error must keep the operation prefix")
	assert.Contains(t, msg, "400", "error must include the status code")
	assert.Contains(t, msg, "model_not_found",
		"error must include the response body so operators can diagnose 400s")
}

func TestOpenAI_ChatTurn_400ErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model 'gpt-4o' is not supported","code":"model_not_found"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL).(ToolCapableProvider)
	_, err := p.ChatTurn(context.Background(),
		[]Message{{Role: RoleUser, Content: "u"}},
		nil,
		CallOptions{Model: "gpt-4o", APIKey: "k"},
		func(StreamChunk) {})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "openai chat_turn", "error must keep the operation prefix")
	assert.Contains(t, msg, "400")
	assert.Contains(t, msg, "model_not_found",
		"ChatTurn must surface the response body for operator diagnosis too")
}

func TestOpenAI_Chat_RetriesOn500UpTo3Times(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "u"}},
		CallOptions{Model: "m", APIKey: "k"},
		func(StreamChunk) {})
	require.Error(t, err)
	// Spec: max 3 retries → 1 initial + 3 retries = 4 attempts total.
	assert.Equal(t, int32(4), atomic.LoadInt32(&attempts),
		"expected 1 initial + 3 retries = 4 total attempts")
}

func TestOpenAI_ChatTurn_ReturnsToolCalls(t *testing.T) {
	srv := startOpenAIFixture(t, fixtureOpenAIToolUse)

	p := NewOpenAI(srv.URL)
	tcp, ok := p.(ToolCapableProvider)
	require.True(t, ok)

	tools := []ToolDef{{
		Name:        "get_weather",
		Description: "return current weather",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}}
	tr, err := tcp.ChatTurn(
		context.Background(),
		[]Message{{Role: RoleUser, Content: "what's the weather in SF?"}},
		tools,
		CallOptions{Model: "gpt-5.1", APIKey: "test", MaxTokens: 1024},
		func(StreamChunk) {},
	)
	require.NoError(t, err)
	require.Len(t, tr.ToolUses, 1)
	assert.Equal(t, "get_weather", tr.ToolUses[0].Name)
	assert.Equal(t, "call_abc", tr.ToolUses[0].ID)
	assert.JSONEq(t, `{"city":"SF"}`, string(tr.ToolUses[0].Input))
	assert.Equal(t, "tool_calls", tr.StopReason)
	assert.Equal(t, 20, tr.Usage.InputTokens)
	assert.Equal(t, 15, tr.Usage.OutputTokens)
}

func TestOpenAI_ChatTurn_TextOnly(t *testing.T) {
	srv := startOpenAIFixture(t, fixtureOpenAITextOnly)

	p := NewOpenAI(srv.URL).(ToolCapableProvider)
	tr, err := p.ChatTurn(
		context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		nil,
		CallOptions{Model: "gpt-5.1", APIKey: "test", MaxTokens: 100},
		func(StreamChunk) {},
	)
	require.NoError(t, err)
	assert.Empty(t, tr.ToolUses)
	assert.Equal(t, "stop", tr.StopReason)
	assert.Equal(t, "Hello!", tr.Text)
}
