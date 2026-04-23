package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

func TestSlack_Publish_PostsTemplatedText(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{
		Secret: "slack_url",
		Text:   "Digest for {{ run.date }}: {{ output.truncated 6 }}",
		Format: "text",
	}}
	tctx := template.Context{RunDate: "2026-04-19", Output: "a very long output indeed"}

	res := p.Publish(context.Background(), dest, "a very long output indeed", tctx,
		mapSecrets{"slack_url": srv.URL})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "Digest for 2026-04-19: a ver…", gotBody["text"])
}

func TestSlack_Publish_SecretMissing(t *testing.T) {
	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{Secret: "missing"}}
	res := p.Publish(context.Background(), dest, "", template.Context{}, mapSecrets{})
	assert.False(t, res.OK)
	assert.Contains(t, res.Err.Error(), "missing")
}

func TestSlack_Publish_DefaultTextIsOutput(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{Secret: "slack_url", Format: "text"}}
	res := p.Publish(context.Background(), dest, "raw output", template.Context{Output: "raw output"},
		mapSecrets{"slack_url": srv.URL})
	require.True(t, res.OK)
	assert.Equal(t, "raw output", gotBody["text"])
}

func TestSlackPublisher_BlockKit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{
		Secret: "hook",
		Format: "blocks",
	}}
	tctx := template.Context{Skill: template.Meta{Name: "test"}, RunDate: "2026-04-22"}
	res := p.Publish(context.Background(), dest, "Hello Slack", tctx, mapSecrets{"hook": srv.URL})
	if !res.OK {
		t.Fatalf("publish failed: %v", res.Err)
	}
	blocks, ok := gotBody["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("expected blocks array in payload, got %v", gotBody)
	}
}

func TestSlackPublisher_TextFallback(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{
		Secret: "hook",
		Format: "text",
	}}
	tctx := template.Context{}
	res := p.Publish(context.Background(), dest, "Plain text", tctx, mapSecrets{"hook": srv.URL})
	if !res.OK {
		t.Fatalf("publish failed: %v", res.Err)
	}
	if _, hasBlocks := gotBody["blocks"]; hasBlocks {
		t.Errorf("format:text should not emit blocks")
	}
	if gotBody["text"] != "Plain text" {
		t.Errorf("text: want 'Plain text', got %v", gotBody["text"])
	}
}
