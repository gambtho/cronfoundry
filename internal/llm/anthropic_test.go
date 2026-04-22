package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anthropicStreamFixture mirrors the SSE event stream from Anthropic's
// Messages API with streaming.
const anthropicStreamFixture = `event: message_start
data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}
`

func TestAnthropic_Chat_StreamsAndReportsUsage(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, anthropicStreamFixture)
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL)
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "hello"}},
		CallOptions{Model: "claude-4.7", MaxTokens: 200, APIKey: "ak-test"},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, "ak-test", gotKey)
	assert.Equal(t, []string{"Hi", " there"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}

// TestAnthropic_Chat_NoSystem verifies that when the caller supplies no system
// message, the adapter omits the System field rather than sending an empty
// TextBlockParam (which the API may warn on or reject).
func TestAnthropic_Chat_NoSystem(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, anthropicStreamFixture)
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hello"}},
		CallOptions{Model: "claude-4.7", MaxTokens: 200, APIKey: "ak-test"},
		func(c StreamChunk) {})

	require.NoError(t, err)
	// When System is nil the SDK's omitempty tags should drop the field
	// entirely. We accept either "absent" or "JSON null"; what we must not
	// see is an empty text block { "system": [{"text":""}] }.
	if sysVal, ok := gotBody["system"]; ok {
		assert.Nil(t, sysVal, "system should be absent or null, not an empty block: %v", sysVal)
	}
}

func TestAnthropic_Chat_RetriesOn500UpTo3Times(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "u"}},
		CallOptions{Model: "m", APIKey: "k"},
		func(StreamChunk) {})
	require.Error(t, err)
	// Spec: max 3 retries → 1 initial + 3 retries = 4 attempts total.
	assert.Equal(t, int32(4), atomic.LoadInt32(&attempts),
		"expected 1 initial + 3 retries = 4 total attempts")
}
