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

func TestTeamsPublisher_RichCard(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := NewTeamsPublisher()
	dest := config.Destination{Teams: &config.WebhookDest{
		Secret: "hook",
		Title:  "Weekly Digest",
		Format: "card",
	}}
	tctx := template.Context{
		Skill:   template.Meta{Name: "digest"},
		RunDate: "2026-04-22",
		RunID:   "run-123",
	}
	res := p.Publish(context.Background(), dest, "Body text", tctx, mapSecrets{"hook": srv.URL})
	if !res.OK {
		t.Fatalf("publish failed: %v", res.Err)
	}
	attachments, ok := gotBody["attachments"].([]any)
	if !ok || len(attachments) == 0 {
		t.Fatalf("expected attachments, got %v", gotBody)
	}
	att := attachments[0].(map[string]any)
	content := att["content"].(map[string]any)
	body := content["body"].([]any)
	// Expect: title TextBlock, FactSet, content TextBlock — at least 3 elements
	if len(body) < 3 {
		t.Errorf("rich card should have at least 3 body elements, got %d", len(body))
	}
	factSet := body[1].(map[string]any)
	if factSet["type"] != "FactSet" {
		t.Errorf("second element should be FactSet, got %v", factSet["type"])
	}
}

func TestTeams_Publish_PostsAdaptiveCard(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	p := NewTeamsPublisher()
	dest := config.Destination{Teams: &config.WebhookDest{
		Secret: "teams",
		Title:  "Weekly digest",
		Text:   "{{ output.truncated 25000 }}",
	}}
	res := p.Publish(context.Background(), dest, "hello", template.Context{Output: "hello"},
		mapSecrets{"teams": srv.URL})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "message", gotBody["type"])
	attachments := gotBody["attachments"].([]any)
	require.Len(t, attachments, 1)
	att := attachments[0].(map[string]any)
	assert.Equal(t, "application/vnd.microsoft.card.adaptive", att["contentType"])
	content := att["content"].(map[string]any)
	assert.Equal(t, "AdaptiveCard", content["type"])
	body := content["body"].([]any)
	require.GreaterOrEqual(t, len(body), 2)
	assert.Equal(t, "Weekly digest", body[0].(map[string]any)["text"])
	assert.Equal(t, "hello", body[1].(map[string]any)["text"])
}
