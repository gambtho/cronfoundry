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

func TestCopilotEnterprise_Chat_SetsRequiredHeaders(t *testing.T) {
	var gotEditorVersion, gotIntegrationID, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEditorVersion = r.Header.Get("Editor-Version")
		gotIntegrationID = r.Header.Get("Copilot-Integration-Id")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIStreamFixture)
	}))
	defer srv.Close()

	p := newCopilotEnterpriseWithBase(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Model: "gpt-4o", APIKey: "ghu_testtoken"},
		func(StreamChunk) {})

	require.NoError(t, err)
	assert.Equal(t, "cronfoundry/1.0", gotEditorVersion)
	assert.Equal(t, "cronfoundry", gotIntegrationID)
	assert.Equal(t, "Bearer ghu_testtoken", gotAuth)
}

func TestCopilotEnterprise_Chat_StreamsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIStreamFixture)
	}))
	defer srv.Close()

	p := newCopilotEnterpriseWithBase(srv.URL)
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Model: "gpt-4o", APIKey: "ghu_testtoken"},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, []string{"Hello", " world"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}

func TestCopilotEnterprise_Chat_ErrorOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
	}))
	defer srv.Close()

	p := newCopilotEnterpriseWithBase(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Model: "gpt-4o", APIKey: "bad"},
		func(StreamChunk) {})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "unauthorized"))
}

func TestCopilotEnterprise_FactoryRegistered(t *testing.T) {
	p, err := NewProvider("copilot-enterprise")
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestCopilotEnterprise_CostCentsIsZero(t *testing.T) {
	cost := CostCents("copilot-enterprise", "gpt-4o", Usage{InputTokens: 1000000, OutputTokens: 1000000})
	assert.Equal(t, 0, cost)
}
