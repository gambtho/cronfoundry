package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSkillFile_HappyPath(t *testing.T) {
	src := []byte(`---
name: weekly-digest
description: Aggregates last week's GitHub activity
model_hint: gpt-5.1
max_tokens: 8000
writeback:
  block_format: xml
---
You are writing a weekly engineering digest.

Memory from prior runs:
{{ include "memory.md" }}
`)

	sk, err := ParseSkillFile(src)
	require.NoError(t, err)

	assert.Equal(t, "weekly-digest", sk.Frontmatter.Name)
	assert.Equal(t, "Aggregates last week's GitHub activity", sk.Frontmatter.Description)
	assert.Equal(t, "gpt-5.1", sk.Frontmatter.ModelHint)
	assert.Equal(t, 8000, sk.Frontmatter.MaxTokens)
	assert.Equal(t, "xml", sk.Frontmatter.Writeback.BlockFormat)
	assert.Contains(t, sk.Body, "You are writing a weekly engineering digest.")
	assert.Contains(t, sk.Body, `{{ include "memory.md" }}`)
}

func TestParseSkillFile_NoFrontmatter(t *testing.T) {
	src := []byte(`Just a prompt with no frontmatter.`)
	_, err := ParseSkillFile(src)
	assert.ErrorContains(t, err, "frontmatter")
}

func TestParseSkillFile_UnterminatedFrontmatter(t *testing.T) {
	src := []byte(`---
name: x
body with no closing fence
`)
	_, err := ParseSkillFile(src)
	assert.ErrorContains(t, err, "unterminated frontmatter")
}
