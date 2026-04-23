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

func TestParseSkillFile_MCPServersAndMaxTurns(t *testing.T) {
	src := []byte(`---
name: weekly-digest
description: d
mcp_servers:
  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
  - name: fetch
    command: uvx
    args: ["mcp-server-fetch"]
max_turns: 30
---
body
`)
	s, err := ParseSkillFile(src)
	require.NoError(t, err)

	require.Len(t, s.Frontmatter.MCPServers, 2)
	assert.Equal(t, "github", s.Frontmatter.MCPServers[0].Name)
	assert.Equal(t, "npx", s.Frontmatter.MCPServers[0].Command)
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-github"}, s.Frontmatter.MCPServers[0].Args)
	assert.Equal(t, "fetch", s.Frontmatter.MCPServers[1].Name)
	assert.Equal(t, 30, s.Frontmatter.MaxTurns)
}

func TestParseSkillFile_RejectsBadMCPServerName(t *testing.T) {
	src := []byte(`---
name: a
mcp_servers:
  - name: BadName
    command: npx
---
body
`)
	s, err := ParseSkillFile(src)
	require.NoError(t, err)
	require.Error(t, s.Validate())
	assert.Contains(t, s.Validate().Error(), "mcp_servers[0].name")
}

func TestParseSkillFile_RejectsDuplicateServerNames(t *testing.T) {
	src := []byte(`---
name: a
mcp_servers:
  - name: github
    command: npx
  - name: github
    command: npx
---
body
`)
	s, err := ParseSkillFile(src)
	require.NoError(t, err)
	require.Error(t, s.Validate())
	assert.Contains(t, s.Validate().Error(), "duplicate mcp_servers name")
}

func TestParseSkillFile_MissingMCPAllowedForNonToolSkill(t *testing.T) {
	src := []byte(`---
name: a
---
body
`)
	s, err := ParseSkillFile(src)
	require.NoError(t, err)
	require.NoError(t, s.Validate())
	assert.Empty(t, s.Frontmatter.MCPServers)
	assert.Zero(t, s.Frontmatter.MaxTurns)
}
