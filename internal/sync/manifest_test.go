package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func TestLoadManifest_ReadsYAMLAndSkills(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cronfoundry.yaml", `version: 1
skills:
  - path: skills/digest
    schedules:
      - name: monday
        cron: "0 9 * * MON"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`)
	writeFile(t, root, "skills/digest/SKILL.md", `---
name: weekly-digest
description: Tiny digest
---
Hello.
`)

	m, skills, err := LoadManifest(root)
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Len(t, m.Skills, 1)

	skill, ok := skills["skills/digest"]
	require.True(t, ok, "skill must be keyed by its path")
	assert.Equal(t, "weekly-digest", skill.Frontmatter.Name)
}

func TestLoadManifest_MissingYAML(t *testing.T) {
	root := t.TempDir()
	_, _, err := LoadManifest(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cronfoundry.yaml")
}

func TestLoadManifest_MissingSKILL(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cronfoundry.yaml", `version: 1
skills:
  - path: skills/missing
    schedules:
      - { name: x, cron: "* * * * *", provider: openai, model: m, destinations: [] }
`)
	_, _, err := LoadManifest(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SKILL.md")
}
