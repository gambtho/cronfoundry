package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectSecretNames_Extracts(t *testing.T) {
	llmRef := "openai_key"
	ctx := runContext{
		Destinations: json.RawMessage(
			`[{"type":"slack","webhook":{"secret":"slack_url"}},{"type":"discord","webhook":{"secret":"discord_url"}}]`),
		Env: json.RawMessage(
			`{"FOO":"bar","API_KEY":{"secret":"api_key"}}`),
		LLMSecretRef: &llmRef,
	}
	names := collectSecretNames(ctx)
	assert.ElementsMatch(t,
		[]string{"slack_url", "discord_url", "api_key", "openai_key"},
		names)
}

func TestCollectSecretNames_Empty(t *testing.T) {
	assert.Empty(t, collectSecretNames(runContext{}))
}

func TestCollectSecretNames_DedupesWithLLMRef(t *testing.T) {
	// If the LLM secret ref is also mentioned in env/destinations, it
	// should appear once.
	llmRef := "shared_key"
	ctx := runContext{
		Env:          json.RawMessage(`{"API":{"secret":"shared_key"}}`),
		LLMSecretRef: &llmRef,
	}
	names := collectSecretNames(ctx)
	assert.ElementsMatch(t, []string{"shared_key"}, names)
}

func TestCollectSecretNames_IgnoresNonStringSecretValues(t *testing.T) {
	// A "secret" whose value isn't a string (e.g., a nested object) should
	// not produce a phantom name entry.
	ctx := runContext{
		Env: json.RawMessage(`{"A":{"secret":{"nested":true}},"B":{"secret":"real"}}`),
	}
	names := collectSecretNames(ctx)
	assert.ElementsMatch(t, []string{"real"}, names)
}

func TestEnvMapForSecrets_UppercasesKeys(t *testing.T) {
	m := envMapForSecrets(map[string]string{
		"slack_url": "https://hooks.slack.com/xyz",
		"api_key":   "sk-abc",
	})
	assert.Equal(t, "https://hooks.slack.com/xyz", m["CRONFOUNDRY_SECRET_SLACK_URL"])
	assert.Equal(t, "sk-abc", m["CRONFOUNDRY_SECRET_API_KEY"])
	assert.Len(t, m, 2)
}

func TestEnvMapForSecrets_Empty(t *testing.T) {
	assert.Empty(t, envMapForSecrets(map[string]string{}))
}

// TestAPIClient_GetRunContext verifies that GetRunContext sends the bearer
// token and decodes the JSON body into the runContext struct.
func TestAPIClient_GetRunContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok-xyz", r.Header.Get("Authorization"))
		assert.Equal(t, "/internal/runs/run-123/context", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"run_id":"run-123",
			"schedule_name":"daily",
			"skill_path":"skills/alpha",
			"skill_sha":"deadbeef",
			"repo":"acme/widgets",
			"repo_id":"repo-77",
			"provider":"openai",
			"model":"gpt-4o-mini"
		}`)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok-xyz", http: ts.Client()}
	got, err := c.GetRunContext(context.Background(), "run-123")
	require.NoError(t, err)
	assert.Equal(t, "daily", got.ScheduleName)
	assert.Equal(t, "skills/alpha", got.SkillPath)
	assert.Equal(t, "deadbeef", got.SkillSha)
	assert.Equal(t, "repo-77", got.RepoID)
	assert.Equal(t, "openai", got.Provider)
}

// TestAPIClient_GetSecrets_Scoped verifies names are URL-encoded and the
// response map is returned intact.
func TestAPIClient_GetSecrets_Scoped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "slack_url,api_key", r.URL.Query().Get("names"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"slack_url":"https://hooks.slack.com/xyz","api_key":"sk-abc"}`)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	got, err := c.GetSecrets(context.Background(), []string{"slack_url", "api_key"})
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/xyz", got["slack_url"])
	assert.Equal(t, "sk-abc", got["api_key"])
}

// TestAPIClient_GetSecrets_EmptyNoRequest ensures the client short-circuits
// without hitting the API when no names are requested.
func TestAPIClient_GetSecrets_EmptyNoRequest(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	got, err := c.GetSecrets(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, hit, "GetSecrets should not issue an HTTP call with no names")
}

func TestAPIClient_GetCloneURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/repos/repo-77/clone-url", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"url":"https://x-access-token:abc@github.com/acme/widgets.git"}`)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	u, err := c.GetCloneURL(context.Background(), "repo-77")
	require.NoError(t, err)
	assert.Equal(t, "https://x-access-token:abc@github.com/acme/widgets.git", u)
}

// TestAPIClient_PostEvents verifies the batched events body shape.
func TestAPIClient_PostEvents(t *testing.T) {
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/internal/runs/run-1/events", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	err := c.PostEvents(context.Background(), "run-1", []event{
		{Type: "runner.start", Level: "info", Payload: map[string]any{"ok": true}},
	})
	require.NoError(t, err)
	events, ok := body["events"].([]any)
	require.True(t, ok)
	require.Len(t, events, 1)
}

// TestAPIClient_PostFinalize verifies the finalize body shape, including
// omission of nil pointer fields.
func TestAPIClient_PostFinalize(t *testing.T) {
	var raw string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/internal/runs/run-1/finalize", r.URL.Path)
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	dur := int32(1234)
	err := c.PostFinalize(context.Background(), "run-1", finalizeRequest{
		Status:     "succeeded",
		DurationMs: &dur,
	})
	require.NoError(t, err)
	assert.Contains(t, raw, `"status":"succeeded"`)
	assert.Contains(t, raw, `"duration_ms":1234`)
	assert.NotContains(t, raw, `"tokens_in"`, "nil pointer fields must be omitted")
}

// TestAPIClient_Do_PropagatesHTTPError ensures non-2xx responses become
// errors carrying the response body.
func TestAPIClient_Do_PropagatesHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	_, err := c.GetRunContext(context.Background(), "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "not found")
}

// TestRunRunnerHTTP_MissingEnv verifies the runner refuses to start without
// the three required env vars.
func TestRunRunnerHTTP_MissingEnv(t *testing.T) {
	t.Setenv(envAPIURL, "")
	t.Setenv(envRunID, "")
	t.Setenv(envRunToken, "")

	err := runRunnerHTTP(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CRONFOUNDRY_API_URL")

	t.Setenv(envAPIURL, "http://127.0.0.1:1")
	err = runRunnerHTTP(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CRONFOUNDRY_RUN_ID")

	t.Setenv(envRunID, "abc")
	err = runRunnerHTTP(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CRONFOUNDRY_RUN_TOKEN")
}

// TestNewRunnerCmd_Hidden verifies the subcommand is registered as hidden —
// operators see it only via --help --all, not the normal help output.
func TestNewRunnerCmd_Hidden(t *testing.T) {
	cmd := newRunnerCmd()
	assert.Equal(t, "runner", cmd.Use)
	assert.True(t, cmd.Hidden, "runner subcommand should be hidden from operator help")
}

