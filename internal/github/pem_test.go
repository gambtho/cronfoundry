package github_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
)

func TestReadPEM_InlineContent(t *testing.T) {
	const inline = "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIB...\n-----END RSA PRIVATE KEY-----\n"
	got, err := github.ReadPEM(inline)
	require.NoError(t, err)
	assert.Equal(t, inline, string(got))
}

func TestReadPEM_InlineContent_WithLeadingWhitespace(t *testing.T) {
	// Azure Container Apps may prepend whitespace when mapping a KV secret
	// into an env var. The helper must still treat leading "-----BEGIN"
	// after trimming as inline content.
	const inline = "\n  -----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n"
	got, err := github.ReadPEM(inline)
	require.NoError(t, err)
	assert.Equal(t, inline, string(got))
}

func TestReadPEM_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.pem")
	const body = "-----BEGIN RSA PRIVATE KEY-----\ndata\n-----END RSA PRIVATE KEY-----\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	got, err := github.ReadPEM(path)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}

func TestReadPEM_FilePath_Missing(t *testing.T) {
	_, err := github.ReadPEM("/nonexistent/app.pem")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "read PEM file"),
		"expected wrapped error, got %v", err)
}
