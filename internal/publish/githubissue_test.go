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

type staticToken string

func (s staticToken) Get(name string) (string, error) { return string(s), nil }

func TestGitHubIssue_Publish_FilesIssueWithTemplatedFields(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/myorg/reports/issues", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/myorg/reports/issues/42","number":42}`))
	}))
	defer srv.Close()

	p := NewGitHubIssuePublisher(srv.URL, "ghp-test")
	dest := config.Destination{GitHubIssue: &config.GitHubIssueDest{
		Repo:      "myorg/reports",
		Title:     "Weekly digest — {{ run.date }}",
		Body:      "{{ output }}",
		Labels:    []string{"digest"},
		Assignees: []string{"alice"},
	}}
	tctx := template.Context{RunDate: "2026-04-19", Output: "summary text"}

	res := p.Publish(context.Background(), dest, "summary text", tctx, staticToken("ghp-test"))

	require.True(t, res.OK, "expected OK, got err: %v", res.Err)
	assert.Equal(t, "token ghp-test", gotAuth)
	assert.Equal(t, "Weekly digest — 2026-04-19", gotBody["title"])
	assert.Equal(t, "summary text", gotBody["body"])
	labels := gotBody["labels"].([]any)
	assert.Equal(t, "digest", labels[0])
	assert.Contains(t, res.Detail, "issues/42")
}

func TestGitHubIssue_Publish_BadRepoFormat(t *testing.T) {
	p := NewGitHubIssuePublisher("", "ghp")
	dest := config.Destination{GitHubIssue: &config.GitHubIssueDest{Repo: "badformat"}}
	res := p.Publish(context.Background(), dest, "", template.Context{}, staticToken("ghp"))
	assert.False(t, res.OK)
	assert.Contains(t, res.Err.Error(), "repo must be owner/name")
}
