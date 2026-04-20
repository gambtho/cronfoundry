package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/secrets"
)

func sig() *object.Signature {
	return &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}
}

// fakeProvider returns a canned streamed response and records the messages it received.
type fakeProvider struct {
	response string
	received []llm.Message
}

func (f *fakeProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	f.received = append([]llm.Message{}, msgs...)
	// Emit in three chunks to exercise streaming.
	for _, chunk := range splitIntoN(f.response, 3) {
		onChunk(llm.StreamChunk{Delta: chunk})
	}
	return llm.Usage{InputTokens: 10, OutputTokens: 20}, nil
}

func splitIntoN(s string, n int) []string {
	if n <= 0 || len(s) == 0 {
		return nil
	}
	size := (len(s) + n - 1) / n
	var out []string
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}

func TestRun_EndToEnd_PublishesAndWritesBack(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := git.PlainInit(repoRoot, false)
	require.NoError(t, err)

	manifestYAML := `
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: mon
        cron: "0 9 * * MON"
        provider: fake
        model: fake-model
        destinations:
          - slack:
              secret: slack_url
        writeback:
          enabled: true
          path: memory.md
          mode: append
        env:
          LOOKBACK_DAYS: "7"
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifestYAML), 0o644))

	skillDir := filepath.Join(repoRoot, "skills/weekly-digest")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillMD := `---
name: weekly-digest
description: test skill
---
Please write a digest using {{ include "notes.md" }}.
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "notes.md"), []byte("NOTES_CONTENT"), 0o644))

	// Seed commit so writeback can append against a tracked tree.
	repo, _ := git.PlainOpen(repoRoot)
	w, _ := repo.Worktree()
	_ = w.AddGlob(".")
	_, err = w.Commit("seed", &git.CommitOptions{Author: sig()})
	require.NoError(t, err)

	// Fake Slack webhook.
	var slackBody map[string]any
	slackSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &slackBody)
		rw.WriteHeader(http.StatusOK)
	}))
	defer slackSrv.Close()

	// Fake LLM provider that emits a response with a memory block.
	fake := &fakeProvider{response: "Weekly summary.\n<memory>learned X</memory>"}
	providerFactory := func(name, _ string) (llm.Provider, error) {
		require.Equal(t, "fake", name)
		return fake, nil
	}

	r := New(Deps{
		ProviderFactory: providerFactory,
		Publishers: map[string]publish.Publisher{
			"slack": publish.NewSlackPublisher(),
		},
	})

	result, err := r.Run(context.Background(), RunInput{
		RepoRoot:     repoRoot,
		ManifestPath: "cronfoundry.yaml",
		SkillPath:    "skills/weekly-digest",
		ScheduleName: "mon",
		Secrets: secrets.New(map[string]string{
			"CRONFOUNDRY_SECRET_SLACK_URL": slackSrv.URL,
		}),
		LLMAPIKey: "sk-test",
		DryRun:    false,
		SkipPush:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.Equal(t, 10, result.Usage.InputTokens)
	assert.Equal(t, 20, result.Usage.OutputTokens)

	// Slack got the published output (memory block stripped).
	assert.Contains(t, slackBody["text"].(string), "Weekly summary.")
	assert.NotContains(t, slackBody["text"].(string), "<memory>")

	// memory.md was updated.
	memContent, err := os.ReadFile(filepath.Join(repoRoot, "memory.md"))
	require.NoError(t, err)
	assert.Contains(t, string(memContent), "learned X")

	// Prompt contained the included file + env banner.
	require.NotEmpty(t, fake.received)
	var all string
	for _, m := range fake.received {
		all += m.Content + "\n---\n"
	}
	assert.Contains(t, all, "NOTES_CONTENT")
	assert.Contains(t, all, "LOOKBACK_DAYS=7")
}

func TestRun_PartialFailure_WhenOneDestinationFails(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := git.PlainInit(repoRoot, false)
	require.NoError(t, err)

	manifest := `
version: 1
skills:
  - path: sk
    schedules:
      - name: s
        cron: "* * * * *"
        provider: fake
        model: m
        destinations:
          - slack: { secret: slack_url }
          - discord: { secret: discord_url }
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifest), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "sk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "sk/SKILL.md"),
		[]byte("---\nname: t\n---\nprompt\n"), 0o644))

	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer slackSrv.Close()
	discordSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer discordSrv.Close()

	fake := &fakeProvider{response: "output text"}
	r := New(Deps{
		ProviderFactory: func(string, string) (llm.Provider, error) { return fake, nil },
		Publishers: map[string]publish.Publisher{
			"slack":   publish.NewSlackPublisher(),
			"discord": publish.NewDiscordPublisher(),
		},
	})

	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repoRoot, ManifestPath: "cronfoundry.yaml",
		SkillPath: "sk", ScheduleName: "s",
		Secrets: secrets.New(map[string]string{
			"CRONFOUNDRY_SECRET_SLACK_URL":   slackSrv.URL,
			"CRONFOUNDRY_SECRET_DISCORD_URL": discordSrv.URL,
		}),
		LLMAPIKey: "k", SkipPush: true,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPartialFailure, result.Status)
	require.Len(t, result.PublishResults, 2)
	okCount := 0
	for _, r := range result.PublishResults {
		if r.OK {
			okCount++
		}
	}
	assert.Equal(t, 1, okCount)
}
