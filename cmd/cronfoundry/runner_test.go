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

// TestRedactCloneURL_StripsUserInfo is the MAJ-4 guard: the token-bearing
// userinfo ("x-access-token:<tok>@") must be stripped from clone URLs
// before they appear in error messages or event payloads.
func TestRedactCloneURL_StripsUserInfo(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "github_install_token",
			in:   "https://x-access-token:ghs_abcdef123@github.com/owner/repo.git",
			want: "https://github.com/owner/repo.git",
		},
		{
			name: "userinfo_with_password",
			in:   "https://user:pw@example.com/repo.git",
			want: "https://example.com/repo.git",
		},
		{
			name: "url_without_userinfo_unchanged",
			in:   "https://github.com/owner/repo.git",
			want: "https://github.com/owner/repo.git",
		},
		{
			name: "unparseable_collapses_to_placeholder",
			in:   "://not a url",
			want: "<clone-url>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, redactCloneURL(tc.in))
		})
	}
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
			"model":"gpt-4o-mini",
			"secret_manifest":["api_tok","openai_key","slack_url"]
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
	assert.Equal(t, []string{"api_tok", "openai_key", "slack_url"}, got.SecretManifest)
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

// TestAPIClient_PostEvents_ManifestSet verifies the shape of a
// manifest.set event the runner posts at run start, declaring the
// allowed secret names.
func TestAPIClient_PostEvents_ManifestSet(t *testing.T) {
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c := &apiClient{baseURL: ts.URL, token: "tok", http: ts.Client()}
	err := c.PostEvents(context.Background(), "run-1", []event{{
		Type:    "manifest.set",
		Level:   "info",
		Payload: map[string]any{"allowed": []string{"api_tok", "slack_url"}},
	}})
	require.NoError(t, err)

	events, ok := body["events"].([]any)
	require.True(t, ok)
	require.Len(t, events, 1)
	first := events[0].(map[string]any)
	assert.Equal(t, "manifest.set", first["type"])
	assert.Equal(t, "info", first["level"])
	payload := first["payload"].(map[string]any)
	allowed := payload["allowed"].([]any)
	assert.Equal(t, []any{"api_tok", "slack_url"}, allowed)
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

// TestAPIClient_PostFinalize_SendsCostCents verifies that cost_cents travels
// through PostFinalize when the field is set.
func TestAPIClient_PostFinalize_SendsCostCents(t *testing.T) {
	var gotBody finalizeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &apiClient{baseURL: srv.URL, token: "t", http: srv.Client()}
	cents := int32(42)
	err := c.PostFinalize(context.Background(), "run-1", finalizeRequest{
		Status:    "succeeded",
		CostCents: &cents,
	})
	require.NoError(t, err)
	require.NotNil(t, gotBody.CostCents)
	assert.Equal(t, int32(42), *gotBody.CostCents)
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
