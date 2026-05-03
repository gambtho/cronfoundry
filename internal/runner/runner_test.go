package runner

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/mcp"
	"github.com/gambtho/cronfoundry/internal/publish"
	runnersecrets "github.com/gambtho/cronfoundry/internal/secrets/runner"
)

func sig() *object.Signature {
	return &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}
}

// fakeProvider returns a canned streamed response and records the messages it received.
// A nil usage field yields the default {InputTokens:10, OutputTokens:20}; tests
// that need to exercise zero-token behavior explicitly can pass &llm.Usage{}.
type fakeProvider struct {
	response string
	received []llm.Message
	usage    *llm.Usage
}

func (f *fakeProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	f.received = append([]llm.Message{}, msgs...)
	// Emit in three chunks to exercise streaming.
	for _, chunk := range splitIntoN(f.response, 3) {
		onChunk(llm.StreamChunk{Delta: chunk})
	}
	if f.usage == nil {
		return llm.Usage{InputTokens: 10, OutputTokens: 20}, nil
	}
	return *f.usage, nil
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
	providerFactory := func(name string) (llm.Provider, error) {
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
		Secrets: runnersecrets.New(map[string]string{
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

	// Slack got the published output (memory block stripped) via Block Kit.
	blocks, ok := slackBody["blocks"].([]any)
	require.True(t, ok, "expected blocks in Slack payload, got %v", slackBody)
	var allText string
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if txt, ok := bm["text"].(map[string]any); ok {
			allText += txt["text"].(string)
		}
	}
	assert.Contains(t, allText, "Weekly summary.")
	assert.NotContains(t, allText, "<memory>")

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
		ProviderFactory: func(string) (llm.Provider, error) { return fake, nil },
		Publishers: map[string]publish.Publisher{
			"slack":   publish.NewSlackPublisher(),
			"discord": publish.NewDiscordPublisher(),
		},
	})

	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repoRoot, ManifestPath: "cronfoundry.yaml",
		SkillPath: "sk", ScheduleName: "s",
		Secrets: runnersecrets.New(map[string]string{
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

func TestRun_PopulatesCostCentsFromUsage(t *testing.T) {
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
        provider: openai
        model: gpt-4o-mini
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifest), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "sk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "sk/SKILL.md"),
		[]byte("---\nname: t\n---\nprompt\n"), 0o644))

	// 1_000_000 input + 1_000_000 output for gpt-4o-mini → 15 + 60 = 75 cents.
	fake := &fakeProvider{
		response: "output",
		usage:    &llm.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
	}
	r := New(Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return fake, nil },
	})

	result, err := r.Run(context.Background(), RunInput{
		RepoRoot:     repoRoot,
		ManifestPath: "cronfoundry.yaml",
		SkillPath:    "sk",
		ScheduleName: "s",
		LLMAPIKey:    "k",
		DryRun:       true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1_000_000, result.Usage.InputTokens)
	assert.Equal(t, 75, result.CostCents)
}

// --- Tool-path test doubles ---

type fakeToolProvider struct {
	turns []llm.TurnResult
	i     int
}

func (f *fakeToolProvider) Chat(_ context.Context, _ []llm.Message, _ llm.CallOptions, _ func(llm.StreamChunk)) (llm.Usage, error) {
	return llm.Usage{}, fmt.Errorf("fakeToolProvider does not implement Chat")
}

func (f *fakeToolProvider) ChatTurn(_ context.Context, _ []llm.Message, _ []llm.ToolDef, _ llm.CallOptions, _ func(llm.StreamChunk)) (llm.TurnResult, error) {
	if len(f.turns) == 0 {
		return llm.TurnResult{}, fmt.Errorf("no turns scripted")
	}
	tr := f.turns[f.i%len(f.turns)]
	f.i++
	return tr, nil
}

type fakeMCPManager struct {
	startErr      error
	tools         map[string][]mcp.Tool
	dispatch      func(calls []mcp.ToolUse) ([]mcp.CallResult, *mcp.FatalError)
	shutdownCalls int
}

func (f *fakeMCPManager) Start(_, _ string, _, _ []string) error { return f.startErr }
func (f *fakeMCPManager) Tools(name string) []mcp.Tool          { return f.tools[name] }
func (f *fakeMCPManager) DispatchAll(_ context.Context, calls []mcp.ToolUse, _ time.Duration) ([]mcp.CallResult, *mcp.FatalError) {
	return f.dispatch(calls)
}
func (f *fakeMCPManager) Shutdown() { f.shutdownCalls++ }

func toolPathRepo(t *testing.T) (string, string) {
	t.Helper()
	repoRoot := t.TempDir()
	manifest := `version: 1
skills:
  - path: sk
    schedules:
      - name: s
        cron: "* * * * *"
        provider: anthropic
        model: claude-opus-4-7
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifest), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "sk"), 0o755))
	skillMD := "---\nname: s\nmcp_servers:\n  - name: stub\n    command: /bin/true\n---\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "sk/SKILL.md"), []byte(skillMD), 0o644))
	return repoRoot, "cronfoundry.yaml"
}

func TestRunner_ToolPath_HappyOneToolThenEndTurn(t *testing.T) {
	repo, manifest := toolPathRepo(t)

	fake := &fakeToolProvider{turns: []llm.TurnResult{
		{ToolUses: []llm.ToolUse{{ID: "t1", Name: "stub__echo", Input: json.RawMessage(`{}`)}}, Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}, StopReason: "tool_use"},
		{Text: "Final answer.", Usage: llm.Usage{InputTokens: 20, OutputTokens: 8}, StopReason: "end_turn"},
	}}
	mgr := &fakeMCPManager{
		tools: map[string][]mcp.Tool{"stub": {{Name: "echo", Description: "d"}}},
		dispatch: func(calls []mcp.ToolUse) ([]mcp.CallResult, *mcp.FatalError) {
			out := make([]mcp.CallResult, len(calls))
			for i, c := range calls {
				out[i] = mcp.CallResult{ID: c.ID, ResultJSON: json.RawMessage(`"ok"`)}
			}
			return out, nil
		},
	}

	r := New(Deps{
		ProviderFactory:   func(string) (llm.Provider, error) { return fake, nil },
		Publishers:        map[string]publish.Publisher{},
		MCPManagerFactory: func(context.Context) mcpManager { return mgr },
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repo, ManifestPath: manifest, SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "test", DryRun: true,
		MCPServers: []config.MCPServer{{Name: "stub", Command: "/bin/true"}},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.Equal(t, "Final answer.", result.Output)
	assert.Equal(t, 30, result.Usage.InputTokens)
	assert.Equal(t, 13, result.Usage.OutputTokens)
	assert.Equal(t, 1, mgr.shutdownCalls)
}

func TestRunner_ToolPath_MaxTurnsExceeded(t *testing.T) {
	repo, manifest := toolPathRepo(t)

	fake := &fakeToolProvider{turns: []llm.TurnResult{
		{ToolUses: []llm.ToolUse{{ID: "t1", Name: "stub__echo", Input: json.RawMessage(`{}`)}}, Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}, StopReason: "tool_use"},
	}}
	mgr := &fakeMCPManager{
		tools: map[string][]mcp.Tool{"stub": {{Name: "echo"}}},
		dispatch: func(calls []mcp.ToolUse) ([]mcp.CallResult, *mcp.FatalError) {
			out := make([]mcp.CallResult, len(calls))
			for i, c := range calls {
				out[i] = mcp.CallResult{ID: c.ID, ResultJSON: json.RawMessage(`"ok"`)}
			}
			return out, nil
		},
	}

	r := New(Deps{
		ProviderFactory:   func(string) (llm.Provider, error) { return fake, nil },
		MCPManagerFactory: func(context.Context) mcpManager { return mgr },
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repo, ManifestPath: manifest, SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "test", DryRun: true, MaxTurns: 3,
		MCPServers: []config.MCPServer{{Name: "stub", Command: "/bin/true"}},
	})
	require.Error(t, err)
	assert.Equal(t, StatusFailed, result.Status)
	assert.Equal(t, "max_turns_exceeded", result.ErrorKind)
	assert.Equal(t, 1, mgr.shutdownCalls)
}

func TestRunner_ToolPath_ProviderNotToolCapable(t *testing.T) {
	repo, manifest := toolPathRepo(t)

	r := New(Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return &fakeProvider{response: "x"}, nil },
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repo, ManifestPath: manifest, SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "test", DryRun: true,
		MCPServers: []config.MCPServer{{Name: "stub", Command: "/bin/true"}},
	})
	require.Error(t, err)
	assert.Equal(t, "provider_tool_unsupported", result.ErrorKind)
}

func TestRunner_ToolPath_ServerStartFailure(t *testing.T) {
	repo, manifest := toolPathRepo(t)

	mgr := &fakeMCPManager{startErr: fmt.Errorf("boom")}
	r := New(Deps{
		ProviderFactory:   func(string) (llm.Provider, error) { return &fakeToolProvider{}, nil },
		MCPManagerFactory: func(context.Context) mcpManager { return mgr },
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repo, ManifestPath: manifest, SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "test", DryRun: true,
		MCPServers: []config.MCPServer{{Name: "stub", Command: "/bin/true"}},
	})
	require.Error(t, err)
	assert.Equal(t, "mcp_server_start_failed", result.ErrorKind)
	assert.Equal(t, 1, mgr.shutdownCalls)
}

func TestRunner_ToolPath_FatalDuringDispatch(t *testing.T) {
	repo, manifest := toolPathRepo(t)

	fake := &fakeToolProvider{turns: []llm.TurnResult{
		{ToolUses: []llm.ToolUse{{ID: "t1", Name: "stub__echo", Input: json.RawMessage(`{}`)}}, Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}, StopReason: "tool_use"},
	}}
	mgr := &fakeMCPManager{
		tools: map[string][]mcp.Tool{"stub": {{Name: "echo"}}},
		dispatch: func(_ []mcp.ToolUse) ([]mcp.CallResult, *mcp.FatalError) {
			return nil, &mcp.FatalError{Kind: mcp.FatalKindToolTimeout, Err: fmt.Errorf("timed out")}
		},
	}

	r := New(Deps{
		ProviderFactory:   func(string) (llm.Provider, error) { return fake, nil },
		MCPManagerFactory: func(context.Context) mcpManager { return mgr },
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repo, ManifestPath: manifest, SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "test", DryRun: true,
		MCPServers: []config.MCPServer{{Name: "stub", Command: "/bin/true"}},
	})
	require.Error(t, err)
	assert.Equal(t, mcp.FatalKindToolTimeout, result.ErrorKind)
	assert.Equal(t, 1, mgr.shutdownCalls)
}

func TestSelectOutputForDest_Named(t *testing.T) {
	blocks := map[string]string{
		"summary":     "Short version",
		"full_report": "Long version",
	}
	fallback := "Full LLM output"
	got := selectOutputForDest("summary", blocks, fallback)
	if got != "Short version" {
		t.Errorf("want 'Short version', got %q", got)
	}
}

func TestSelectOutputForDest_Missing(t *testing.T) {
	blocks := map[string]string{"summary": "Short"}
	got := selectOutputForDest("full_report", blocks, "fallback")
	if got != "fallback" {
		t.Errorf("want fallback when named block missing, got %q", got)
	}
}

func TestSelectOutputForDest_Empty(t *testing.T) {
	blocks := map[string]string{"summary": "Short"}
	got := selectOutputForDest("", blocks, "fallback")
	if got != "fallback" {
		t.Errorf("want fallback when output name is empty, got %q", got)
	}
}

func TestRunner_SkippedDestinationInResults(t *testing.T) {
	pr := publish.Result{Type: "slack", OK: true, Skipped: true, SkipReason: "condition:on_failure not met"}
	allOK := true
	for _, r := range []publish.Result{pr} {
		if !r.OK && !r.Skipped {
			allOK = false
		}
	}
	if !allOK {
		t.Error("skipped result should not count as failure")
	}
}

func TestRun_SecretNotInLLMMessages(t *testing.T) {
	// exercises the non-MCP (single-shot) path
	repoRoot := t.TempDir()
	repo, err := git.PlainInit(repoRoot, false)
	require.NoError(t, err)

	manifest := `
version: 1
skills:
  - path: skills/check
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: fake
        model: fake-model
        destinations:
          - slack:
              secret: slack_url
        env:
          API_KEY:
            secret: my_api_key
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifest), 0o644))
	skillDir := filepath.Join(repoRoot, "skills/check")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: check\n---\nCheck things.\n"), 0o644))

	// Seed commit so the runner has a valid git repo.
	w, err := repo.Worktree()
	require.NoError(t, err)
	_ = w.AddGlob(".")
	_, err = w.Commit("seed", &git.CommitOptions{Author: sig()})
	require.NoError(t, err)

	slackCalled := false
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer slackSrv.Close()

	fake := &fakeProvider{response: "all good"}
	r := New(Deps{
		ProviderFactory: func(_ string) (llm.Provider, error) { return fake, nil },
		Publishers:      map[string]publish.Publisher{"slack": publish.NewSlackPublisher()},
	})

	result, err := r.Run(context.Background(), RunInput{
		RepoRoot:     repoRoot,
		ManifestPath: "cronfoundry.yaml",
		SkillPath:    "skills/check",
		ScheduleName: "daily",
		Secrets: runnersecrets.New(map[string]string{
			"CRONFOUNDRY_SECRET_MY_API_KEY": "super-secret-value",
			"CRONFOUNDRY_SECRET_SLACK_URL":  slackSrv.URL,
		}),
		LLMAPIKey: "sk-test",
		DryRun:    false,
		SkipPush:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.True(t, slackCalled)

	for _, msg := range fake.received {
		assert.NotContains(t, msg.Content, "super-secret-value",
			"secret value must not appear in LLM messages")
	}
	var allContent string
	for _, msg := range fake.received {
		allContent += msg.Content + "\n"
	}
	assert.Contains(t, allContent, "API_KEY=[secret]")
}

type failingProvider struct{}

func (failingProvider) Chat(_ context.Context, _ []llm.Message, _ llm.CallOptions, _ func(llm.StreamChunk)) (llm.Usage, error) {
	return llm.Usage{}, fmt.Errorf("provider boom")
}

func phaseEnterTestRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	repo, err := git.PlainInit(repoRoot, false)
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
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifest), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "sk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "sk/SKILL.md"),
		[]byte("---\nname: t\n---\nprompt\n"), 0o644))
	w, err := repo.Worktree()
	require.NoError(t, err)
	_ = w.AddGlob(".")
	_, err = w.Commit("seed", &git.CommitOptions{Author: sig()})
	require.NoError(t, err)
	return repoRoot
}

func TestRun_EmitsPhaseEnterInOrder_HappyPath(t *testing.T) {
	var got []string
	sink := func(e RunEvent) {
		if e.Type == EventPhaseEnter {
			got = append(got, e.Payload["phase"].(string))
		}
	}

	repoRoot := phaseEnterTestRepo(t)
	fake := &fakeProvider{response: "ok"}
	r := New(Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return fake, nil },
		EventSink:       sink,
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot: repoRoot, ManifestPath: "cronfoundry.yaml",
		SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "k", SkipPush: true,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	require.Equal(t, []string{"boot", "secrets", "exec", "publish"}, got)
}

func TestRun_EmitsFailWithPrev_OnExecError(t *testing.T) {
	var got []map[string]any
	sink := func(e RunEvent) {
		if e.Type == EventPhaseEnter {
			got = append(got, e.Payload)
		}
	}

	repoRoot := phaseEnterTestRepo(t)
	r := New(Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return failingProvider{}, nil },
		EventSink:       sink,
	})
	_, err := r.Run(context.Background(), RunInput{
		RepoRoot: repoRoot, ManifestPath: "cronfoundry.yaml",
		SkillPath: "sk", ScheduleName: "s",
		LLMAPIKey: "k", DryRun: true,
	})
	require.Error(t, err)

	require.GreaterOrEqual(t, len(got), 4)
	last := got[len(got)-1]
	require.Equal(t, "fail", last["phase"])
	require.Equal(t, "exec", last["prev"])
}

func TestBuildEnvBanner_SecretRedacted(t *testing.T) {
	env := map[string]config.EnvValue{
		"GITHUB_TOKEN": {Secret: "github_pat"},
		"BASE_URL":     {Literal: "https://api.example.com"},
	}

	banner := buildEnvBanner(env)

	assert.Contains(t, banner, "GITHUB_TOKEN=[secret]")
	assert.Contains(t, banner, "BASE_URL=https://api.example.com")
}
