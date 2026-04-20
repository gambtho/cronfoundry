package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

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

// TestGitHubIssue_Publish_TruncatesAtRuneBoundary verifies that when the body
// exceeds the 64 KB cap and the byte-level cut point lands mid-rune, the
// truncated output contains no U+FFFD replacement characters. We construct a
// body whose multibyte runes straddle the cut boundary.
func TestGitHubIssue_Publish_TruncatesAtRuneBoundary(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/myorg/reports/issues/1","number":1}`))
	}))
	defer srv.Close()

	// Build a body that is larger than issueBodyMaxBytes (64 KB) and places
	// a 3-byte rune straddling the byte-level cut point. The truncator slices
	// at issueBodyMaxBytes-64. "日" is 3 bytes, so we choose a pad length such
	// that the last rune in the prefix ends one byte past the cut, leaving
	// the prefix ending in a truncated (invalid) UTF-8 byte sequence.
	padLen := issueBodyMaxBytes - 64 - 29 // 29 ≡ 2 (mod 3), so cut splits a rune
	multibyte := strings.Repeat("日本語", 200)
	large := strings.Repeat("a", padLen) + multibyte + strings.Repeat("b", 1024)
	require.Greater(t, len(large), issueBodyMaxBytes)
	require.False(t, utf8.ValidString(large[:issueBodyMaxBytes-64]),
		"test fixture must straddle a rune boundary at the cut point")

	p := NewGitHubIssuePublisher(srv.URL, "ghp-test")
	dest := config.Destination{GitHubIssue: &config.GitHubIssueDest{
		Repo:  "myorg/reports",
		Title: "title",
	}}
	res := p.Publish(context.Background(), dest, large, template.Context{}, staticToken("ghp-test"))
	require.True(t, res.OK, "expected OK, got err: %v", res.Err)

	got, _ := gotBody["body"].(string)
	require.NotEmpty(t, got)
	assert.NotContains(t, got, "\uFFFD", "truncated body must not contain U+FFFD replacement character")
	assert.True(t, strings.HasSuffix(got, "... [truncated, full output in run log]"))
}

func TestGitHubIssue_Publish_BadRepoFormat(t *testing.T) {
	p := NewGitHubIssuePublisher("", "ghp")
	dest := config.Destination{GitHubIssue: &config.GitHubIssueDest{Repo: "badformat"}}
	res := p.Publish(context.Background(), dest, "", template.Context{}, staticToken("ghp"))
	assert.False(t, res.OK)
	assert.Contains(t, res.Err.Error(), "repo must be owner/name")
}
