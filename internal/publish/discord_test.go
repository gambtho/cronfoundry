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

func TestDiscord_Publish_PostsContentAndUsername(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusNoContent) // Discord returns 204
	}))
	defer srv.Close()

	p := NewDiscordPublisher()
	dest := config.Destination{Discord: &config.WebhookDest{
		Secret:   "disc",
		Content:  "{{ output.truncated 1900 }}",
		Username: "CronFoundry",
	}}
	out := "short output"
	res := p.Publish(context.Background(), dest, out, template.Context{Output: out},
		mapSecrets{"disc": srv.URL})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "short output", gotBody["content"])
	assert.Equal(t, "CronFoundry", gotBody["username"])
}

func TestDiscord_Publish_EnforcesHardLimit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := NewDiscordPublisher()
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'a'
	}
	dest := config.Destination{Discord: &config.WebhookDest{Secret: "disc"}}
	res := p.Publish(context.Background(), dest, string(long), template.Context{Output: string(long)},
		mapSecrets{"disc": srv.URL})
	require.True(t, res.OK)
	content := gotBody["content"].(string)
	assert.LessOrEqual(t, len([]rune(content)), 2000)
	assert.True(t, len([]rune(content)) > 1900)
}
