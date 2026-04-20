package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
