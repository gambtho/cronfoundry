package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveIncludes_HappyPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte("CONTENT_A"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "b.md"), []byte("CONTENT_B"), 0o644))

	body := `Before
{{ include "a.md" }}
Middle
{{ include "sub/b.md" }}
After`

	out, err := ResolveIncludes(body, root)
	require.NoError(t, err)
	assert.Contains(t, out, "Before")
	assert.Contains(t, out, "CONTENT_A")
	assert.Contains(t, out, "Middle")
	assert.Contains(t, out, "CONTENT_B")
	assert.Contains(t, out, "After")
}

func TestResolveIncludes_MissingFile(t *testing.T) {
	root := t.TempDir()
	_, err := ResolveIncludes(`{{ include "missing.md" }}`, root)
	assert.ErrorContains(t, err, "missing.md")
}

func TestResolveIncludes_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	_, err := ResolveIncludes(`{{ include "../etc/passwd" }}`, root)
	assert.ErrorContains(t, err, "outside root")
}

func TestResolveIncludes_RejectsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	_, err := ResolveIncludes(`{{ include "/etc/passwd" }}`, root)
	assert.ErrorContains(t, err, "absolute")
}

func TestResolveIncludes_NoRecursion(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte(`{{ include "b.md" }}`), 0o644))
	out, err := ResolveIncludes(`{{ include "a.md" }}`, root)
	require.NoError(t, err)
	assert.Equal(t, `{{ include "b.md" }}`, out)
}
