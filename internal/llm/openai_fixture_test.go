package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fixtureOpenAIToolUse simulates an SSE stream where the model invokes a tool.
const fixtureOpenAIToolUse = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"SF\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":15,"total_tokens":35}}

data: [DONE]
`

// fixtureOpenAITextOnly simulates a text-only streaming response.
const fixtureOpenAITextOnly = `data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":"Hello!"},"finish_reason":null}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-5.1","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}

data: [DONE]
`

func startOpenAIFixture(t *testing.T, fixture string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, fixture)
	}))
	t.Cleanup(srv.Close)
	return srv
}
