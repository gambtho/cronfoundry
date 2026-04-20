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

// Azure OpenAI streaming uses the same SSE chunk format as OpenAI's, but the path
// is /openai/deployments/{deployment}/chat/completions with an api-version query
// and `api-key` header (no Bearer).
func TestAzureFoundry_Chat_StreamsAndReportsUsage(t *testing.T) {
	var gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("api-key")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIStreamFixture) // reuses the fixture from openai_test.go
	}))
	defer srv.Close()

	p := NewAzureFoundry()
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{
			Model:      "gpt-5.1", // ignored by Azure in favor of Deployment
			Deployment: "prod-gpt",
			APIKey:     "azkey",
			Endpoint:   srv.URL,
		},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, "azkey", gotKey)
	assert.True(t, strings.Contains(gotPath, "/openai/deployments/prod-gpt/chat/completions"),
		"path was %q", gotPath)
	assert.Equal(t, []string{"Hello", " world"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}

func TestAzureFoundry_MissingEndpoint(t *testing.T) {
	p := NewAzureFoundry()
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Deployment: "d", APIKey: "k"},
		func(StreamChunk) {})
	assert.ErrorContains(t, err, "endpoint")
}

func TestAzureFoundry_MissingDeployment(t *testing.T) {
	p := NewAzureFoundry()
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Endpoint: "https://x", APIKey: "k"},
		func(StreamChunk) {})
	assert.ErrorContains(t, err, "deployment")
}
