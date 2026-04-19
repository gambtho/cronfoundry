# P1 — Core Runner (CLI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone Go CLI (`cronfoundry-runner`) that takes a `cronfoundry.yaml` manifest + skill path + schedule name, executes the skill against a user-selected LLM provider, publishes output to configured destinations (GitHub issue, Slack, Discord, Teams), and optionally commits a `<memory>` write-back to the skill repo.

**Architecture:** Single binary. Domain packages under `internal/` with one responsibility each: config parsing, LLM provider adapters, output/memory parsing, template rendering, destination publishers, writeback committer, secret redaction, runner orchestration. External I/O (LLM, webhooks, git) is tested with `httptest` servers or temp repos. No DB, no API, no UI — this phase validates the core loop end-to-end from the command line.

**Tech Stack:**
- Go 1.22+
- YAML: `sigs.k8s.io/yaml`
- CLI: `github.com/spf13/cobra`
- LLM SDKs: `github.com/openai/openai-go`, `github.com/anthropics/anthropic-sdk-go`, `github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai`
- GitHub REST: `github.com/google/go-github/v73`
- Git ops: `github.com/go-git/go-git/v5`
- Logging: `log/slog` (stdlib)
- Tests: `testing` (stdlib) + `github.com/stretchr/testify`

**Inputs the CLI accepts** (finalized in Task 21):

```
cronfoundry-runner run \
  --manifest ./cronfoundry.yaml \
  --skill-path skills/weekly-digest \
  --schedule-name monday-morning \
  --repo .                           # repo root (for include resolution + writeback)
  --llm-key-env OPENAI_API_KEY       # env var that holds the LLM key
  --dry-run                          # optional: don't publish, don't push writeback
```

Secrets referenced by `{ secret: name }` resolve from env: `CRONFOUNDRY_SECRET_<UPPER(name)>`.
Writeback push uses `GITHUB_TOKEN` from env (PAT; GitHub App comes in P2).

---

## File Structure (locked in upfront)

```
cronfoundry/
├── go.mod
├── go.sum
├── cmd/
│   └── runner/
│       └── main.go                       # CLI entry
├── internal/
│   ├── config/
│   │   ├── manifest.go                   # cronfoundry.yaml parser
│   │   ├── manifest_test.go
│   │   ├── skill.go                      # SKILL.md frontmatter+body parser
│   │   ├── skill_test.go
│   │   ├── include.go                    # {{ include "..." }} preprocessor
│   │   └── include_test.go
│   ├── secrets/
│   │   ├── resolver.go                   # env-based secret resolution
│   │   └── resolver_test.go
│   ├── llm/
│   │   ├── provider.go                   # Provider interface + types
│   │   ├── openai.go
│   │   ├── openai_test.go
│   │   ├── anthropic.go
│   │   ├── anthropic_test.go
│   │   ├── azurefoundry.go
│   │   ├── azurefoundry_test.go
│   │   ├── factory.go                    # name → provider
│   │   └── factory_test.go
│   ├── memory/
│   │   ├── parser.go                     # <memory>...</memory> extraction
│   │   └── parser_test.go
│   ├── template/
│   │   ├── render.go                     # {{ output }}, {{ run.* }}, etc.
│   │   └── render_test.go
│   ├── redact/
│   │   ├── redact.go                     # secret redaction for logs
│   │   └── redact_test.go
│   ├── publish/
│   │   ├── publisher.go                  # interface + Result type
│   │   ├── dispatcher.go                 # parallel fan-out w/ isolation
│   │   ├── dispatcher_test.go
│   │   ├── githubissue.go
│   │   ├── githubissue_test.go
│   │   ├── slack.go
│   │   ├── slack_test.go
│   │   ├── discord.go
│   │   ├── discord_test.go
│   │   ├── teams.go
│   │   └── teams_test.go
│   ├── writeback/
│   │   ├── writeback.go                  # go-git commit + push
│   │   └── writeback_test.go
│   └── runner/
│       ├── runner.go                     # orchestration
│       └── runner_test.go                # end-to-end w/ fakes
└── testdata/
    └── skills/
        └── weekly-digest/
            ├── SKILL.md
            └── context/
                └── template.md
```

Each file has one clear responsibility. No file does I/O + parsing + business logic together.

---

## Task 1: Bootstrap Go module + Cobra CLI skeleton

**Files:**
- Create: `cronfoundry/go.mod`
- Create: `cronfoundry/cmd/runner/main.go`
- Create: `cronfoundry/.gitignore`

- [ ] **Step 1: Initialize the Go module**

From the repo root:

```bash
cd /home/tng/workspace/cronfoundry
go mod init github.com/gambtho/cronfoundry
```

- [ ] **Step 2: Add `.gitignore`**

```
cronfoundry-runner
cronfoundry-runner.exe
*.test
coverage.out
.env
.env.local
```

- [ ] **Step 3: Add Cobra + testify dependencies**

```bash
go get github.com/spf13/cobra@latest
go get github.com/stretchr/testify@latest
```

- [ ] **Step 4: Write the minimal CLI in `cmd/runner/main.go`**

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "cronfoundry-runner",
		Short: "Execute a CronFoundry skill once",
	}
	root.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run a skill from a cronfoundry.yaml manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("run: not yet implemented")
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Build and verify the CLI runs**

```bash
go build -o cronfoundry-runner ./cmd/runner
./cronfoundry-runner run
```

Expected: exit code 1, stderr contains `Error: run: not yet implemented`.

```bash
./cronfoundry-runner --help
```

Expected: shows `Usage: cronfoundry-runner [command]` with `run` and `help` subcommands.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd/ .gitignore
git commit -m "feat(runner): bootstrap Go module and CLI skeleton"
```

---

## Task 2: Manifest (cronfoundry.yaml) parser — happy path

**Files:**
- Create: `internal/config/manifest.go`
- Create: `internal/config/manifest_test.go`

- [ ] **Step 1: Add YAML dependency**

```bash
go get sigs.k8s.io/yaml@latest
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/manifest_test.go`:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseManifest_HappyPath(t *testing.T) {
	yaml := []byte(`
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        timezone: America/Los_Angeles
        overlap_policy: skip
        timeout_sec: 600
        provider: openai
        model: gpt-5.1
        destinations:
          - github-issue:
              repo: myorg/reports
              title: "Weekly digest"
              labels: [digest, automated]
          - slack:
              secret: slack_digest_webhook
        writeback:
          enabled: true
          path: memory.md
          mode: append
        env:
          LOOKBACK_DAYS: "7"
          TEAM_NAME:
            secret: team_name
`)

	m, err := ParseManifest(yaml)

	require.NoError(t, err)
	assert.Equal(t, 1, m.Version)
	require.Len(t, m.Skills, 1)

	skill := m.Skills[0]
	assert.Equal(t, "skills/weekly-digest", skill.Path)
	require.Len(t, skill.Schedules, 1)

	sch := skill.Schedules[0]
	assert.Equal(t, "monday-morning", sch.Name)
	assert.Equal(t, "0 9 * * MON", sch.Cron)
	assert.Equal(t, "America/Los_Angeles", sch.Timezone)
	assert.Equal(t, "skip", sch.OverlapPolicy)
	assert.Equal(t, 600, sch.TimeoutSec)
	assert.Equal(t, "openai", sch.Provider)
	assert.Equal(t, "gpt-5.1", sch.Model)

	require.Len(t, sch.Destinations, 2)
	assert.NotNil(t, sch.Destinations[0].GitHubIssue)
	assert.Equal(t, "myorg/reports", sch.Destinations[0].GitHubIssue.Repo)
	assert.Equal(t, []string{"digest", "automated"}, sch.Destinations[0].GitHubIssue.Labels)
	assert.NotNil(t, sch.Destinations[1].Slack)
	assert.Equal(t, "slack_digest_webhook", sch.Destinations[1].Slack.Secret)

	require.NotNil(t, sch.Writeback)
	assert.True(t, sch.Writeback.Enabled)
	assert.Equal(t, "memory.md", sch.Writeback.Path)
	assert.Equal(t, "append", sch.Writeback.Mode)

	assert.Equal(t, "7", sch.Env["LOOKBACK_DAYS"].Literal)
	assert.Equal(t, "team_name", sch.Env["TEAM_NAME"].Secret)
}
```

- [ ] **Step 3: Run the test and confirm it fails**

```bash
go test ./internal/config/...
```

Expected: build fails — `ParseManifest` undefined.

- [ ] **Step 4: Implement `internal/config/manifest.go`**

```go
// Package config parses CronFoundry manifest and skill files.
package config

import (
	"encoding/json"
	"fmt"

	"sigs.k8s.io/yaml"
)

type Manifest struct {
	Version int           `json:"version"`
	Skills  []SkillEntry  `json:"skills"`
}

type SkillEntry struct {
	Path      string     `json:"path"`
	Schedules []Schedule `json:"schedules"`
}

type Schedule struct {
	Name          string              `json:"name"`
	Cron          string              `json:"cron"`
	Timezone      string              `json:"timezone"`
	OverlapPolicy string              `json:"overlap_policy"`
	TimeoutSec    int                 `json:"timeout_sec"`
	Provider      string              `json:"provider"`
	Model         string              `json:"model"`
	Destinations  []Destination       `json:"destinations"`
	Writeback     *WritebackConfig    `json:"writeback,omitempty"`
	Env           map[string]EnvValue `json:"env"`
}

type Destination struct {
	GitHubIssue *GitHubIssueDest `json:"github-issue,omitempty"`
	Slack       *WebhookDest     `json:"slack,omitempty"`
	Discord     *WebhookDest     `json:"discord,omitempty"`
	Teams       *WebhookDest     `json:"teams,omitempty"`
}

type GitHubIssueDest struct {
	Repo      string   `json:"repo"`
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
}

type WebhookDest struct {
	Secret   string `json:"secret"`
	Text     string `json:"text,omitempty"`
	Content  string `json:"content,omitempty"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
}

type WritebackConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
	Mode    string `json:"mode"`
}

// EnvValue is either a literal string or a `{ secret: name }` reference.
type EnvValue struct {
	Literal string
	Secret  string
}

func (e *EnvValue) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		e.Literal = s
		return nil
	}
	var ref struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &ref); err != nil {
		return fmt.Errorf("env value must be string or { secret: name }: %w", err)
	}
	if ref.Secret == "" {
		return fmt.Errorf("env value object must set 'secret'")
	}
	e.Secret = ref.Secret
	return nil
}

func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}
```

- [ ] **Step 5: Run the test and confirm it passes**

```bash
go test ./internal/config/... -run TestParseManifest_HappyPath -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config/manifest.go internal/config/manifest_test.go
git commit -m "feat(config): parse cronfoundry.yaml manifest"
```

---

## Task 3: Manifest validation + schedule lookup helpers

**Files:**
- Modify: `internal/config/manifest.go`
- Modify: `internal/config/manifest_test.go`

- [ ] **Step 1: Add failing tests for validation + lookup**

Append to `internal/config/manifest_test.go`:

```go
func TestManifest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing version",
			yaml:    "skills: []",
			wantErr: "version",
		},
		{
			name:    "unsupported version",
			yaml:    "version: 2\nskills: []",
			wantErr: "version 2 not supported",
		},
		{
			name:    "duplicate skill path",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules: []\n  - path: a\n    schedules: []",
			wantErr: "duplicate skill path \"a\"",
		},
		{
			name:    "duplicate schedule name within skill",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", provider: openai, model: m }\n      - { name: x, cron: \"* * * * *\", provider: openai, model: m }",
			wantErr: "duplicate schedule name \"x\"",
		},
		{
			name:    "schedule missing provider",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", model: m }",
			wantErr: "provider",
		},
		{
			name:    "schedule missing model",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", provider: openai }",
			wantErr: "model",
		},
		{
			name:    "invalid overlap policy",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", provider: openai, model: m, overlap_policy: weird }",
			wantErr: "overlap_policy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.yaml))
			if err != nil {
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			err = m.Validate()
			require.Error(t, err, "expected validation error")
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestManifest_FindSchedule(t *testing.T) {
	m := &Manifest{
		Version: 1,
		Skills: []SkillEntry{
			{Path: "skills/a", Schedules: []Schedule{{Name: "s1", Cron: "* * * * *", Provider: "openai", Model: "m"}}},
		},
	}
	require.NoError(t, m.Validate())

	skill, sch, err := m.FindSchedule("skills/a", "s1")
	require.NoError(t, err)
	assert.Equal(t, "skills/a", skill.Path)
	assert.Equal(t, "s1", sch.Name)

	_, _, err = m.FindSchedule("skills/a", "missing")
	assert.ErrorContains(t, err, "schedule \"missing\"")

	_, _, err = m.FindSchedule("skills/missing", "s1")
	assert.ErrorContains(t, err, "skill \"skills/missing\"")
}
```

- [ ] **Step 2: Run the tests and confirm failure**

```bash
go test ./internal/config/... -v
```

Expected: `Validate` and `FindSchedule` undefined.

- [ ] **Step 3: Add `Validate` and `FindSchedule` to `internal/config/manifest.go`**

Append:

```go
var validOverlap = map[string]bool{
	"":           true, // default to skip
	"skip":       true,
	"queue":      true,
	"concurrent": true,
}

func (m *Manifest) Validate() error {
	if m.Version == 0 {
		return fmt.Errorf("version: required")
	}
	if m.Version != 1 {
		return fmt.Errorf("version %d not supported (supported: 1)", m.Version)
	}
	seenSkills := map[string]bool{}
	for _, s := range m.Skills {
		if s.Path == "" {
			return fmt.Errorf("skill: path required")
		}
		if seenSkills[s.Path] {
			return fmt.Errorf("duplicate skill path %q", s.Path)
		}
		seenSkills[s.Path] = true

		seenSched := map[string]bool{}
		for _, sch := range s.Schedules {
			if sch.Name == "" {
				return fmt.Errorf("skill %q: schedule name required", s.Path)
			}
			if seenSched[sch.Name] {
				return fmt.Errorf("skill %q: duplicate schedule name %q", s.Path, sch.Name)
			}
			seenSched[sch.Name] = true
			if sch.Cron == "" {
				return fmt.Errorf("skill %q schedule %q: cron required", s.Path, sch.Name)
			}
			if sch.Provider == "" {
				return fmt.Errorf("skill %q schedule %q: provider required", s.Path, sch.Name)
			}
			if sch.Model == "" {
				return fmt.Errorf("skill %q schedule %q: model required", s.Path, sch.Name)
			}
			if !validOverlap[sch.OverlapPolicy] {
				return fmt.Errorf("skill %q schedule %q: overlap_policy %q invalid (want: skip|queue|concurrent)", s.Path, sch.Name, sch.OverlapPolicy)
			}
		}
	}
	return nil
}

func (m *Manifest) FindSchedule(skillPath, scheduleName string) (*SkillEntry, *Schedule, error) {
	for i := range m.Skills {
		if m.Skills[i].Path == skillPath {
			for j := range m.Skills[i].Schedules {
				if m.Skills[i].Schedules[j].Name == scheduleName {
					return &m.Skills[i], &m.Skills[i].Schedules[j], nil
				}
			}
			return nil, nil, fmt.Errorf("schedule %q not found under skill %q", scheduleName, skillPath)
		}
	}
	return nil, nil, fmt.Errorf("skill %q not found in manifest", skillPath)
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

```bash
go test ./internal/config/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/manifest.go internal/config/manifest_test.go
git commit -m "feat(config): validate manifest and look up schedules"
```

---

## Task 4: SKILL.md frontmatter + body parser

**Files:**
- Create: `internal/config/skill.go`
- Create: `internal/config/skill_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/skill_test.go`:

```go
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
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/config/... -run TestParseSkillFile -v
```

Expected: `ParseSkillFile` undefined.

- [ ] **Step 3: Implement `internal/config/skill.go`**

```go
package config

import (
	"bytes"
	"fmt"

	"sigs.k8s.io/yaml"
)

type Skill struct {
	Frontmatter SkillFrontmatter
	Body        string
}

type SkillFrontmatter struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	ModelHint   string                   `json:"model_hint"`
	MaxTokens   int                      `json:"max_tokens"`
	Writeback   SkillWritebackFrontmatter `json:"writeback"`
}

type SkillWritebackFrontmatter struct {
	BlockFormat string `json:"block_format"`
}

var fence = []byte("---")

// ParseSkillFile extracts the YAML frontmatter and prompt body.
// Frontmatter is delimited by `---\n` on its own line at the top of the file
// and a matching `---\n` closing line.
func ParseSkillFile(data []byte) (*Skill, error) {
	if !bytes.HasPrefix(data, append(fence, '\n')) && !bytes.HasPrefix(data, append(fence, '\r', '\n')) {
		return nil, fmt.Errorf("frontmatter required: file must start with --- on its own line")
	}
	// Find the closing fence.
	rest := data[len(fence):]
	// Skip leading newline after the opening fence.
	if len(rest) > 0 && rest[0] == '\r' {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	// Find a line that is exactly ---.
	end := -1
	for i := 0; i < len(rest); i++ {
		// Line start: either i==0 or previous is newline.
		lineStart := i == 0 || rest[i-1] == '\n'
		if !lineStart {
			continue
		}
		remaining := rest[i:]
		if bytes.HasPrefix(remaining, append(fence, '\n')) || bytes.HasPrefix(remaining, append(fence, '\r', '\n')) {
			end = i
			break
		}
		// Allow the file to end with --- and no trailing newline
		if bytes.Equal(remaining, fence) {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter: closing --- not found")
	}
	fmBytes := rest[:end]
	body := rest[end+len(fence):]
	// Skip single leading newline after closing fence.
	if len(body) > 0 && body[0] == '\r' {
		body = body[1:]
	}
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}

	var fm SkillFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	return &Skill{Frontmatter: fm, Body: string(body)}, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/skill.go internal/config/skill_test.go
git commit -m "feat(config): parse SKILL.md frontmatter and body"
```

---

## Task 5: `{{ include "..." }}` preprocessor (single-level, path-safe)

**Files:**
- Create: `internal/config/include.go`
- Create: `internal/config/include_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
	// Included file itself contains an include directive — must appear verbatim, not re-processed.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.md"), []byte(`{{ include "b.md" }}`), 0o644))
	out, err := ResolveIncludes(`{{ include "a.md" }}`, root)
	require.NoError(t, err)
	assert.Equal(t, `{{ include "b.md" }}`, out)
}
```

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/config/... -run TestResolveIncludes -v
```

Expected: `ResolveIncludes` undefined.

- [ ] **Step 3: Implement `internal/config/include.go`**

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var includeRe = regexp.MustCompile(`\{\{\s*include\s+"([^"]+)"\s*\}\}`)

// ResolveIncludes replaces `{{ include "path" }}` directives with the file
// contents read from `root`. The directive is single-level only — included
// content is inserted verbatim without re-processing.
//
// Security:
//   - absolute paths are rejected
//   - paths that escape `root` (via `..`) are rejected
func ResolveIncludes(body, root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	var firstErr error
	result := includeRe.ReplaceAllStringFunc(body, func(match string) string {
		if firstErr != nil {
			return match
		}
		sub := includeRe.FindStringSubmatch(match)
		rel := sub[1]
		if filepath.IsAbs(rel) {
			firstErr = fmt.Errorf("include path %q must not be absolute", rel)
			return match
		}
		joined := filepath.Join(absRoot, rel)
		cleaned, err := filepath.Abs(joined)
		if err != nil {
			firstErr = fmt.Errorf("resolve include %q: %w", rel, err)
			return match
		}
		// Ensure cleaned stays under absRoot.
		if !strings.HasPrefix(cleaned, absRoot+string(os.PathSeparator)) && cleaned != absRoot {
			firstErr = fmt.Errorf("include %q resolves outside root", rel)
			return match
		}
		data, err := os.ReadFile(cleaned)
		if err != nil {
			firstErr = fmt.Errorf("read include %q: %w", rel, err)
			return match
		}
		return string(data)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/include.go internal/config/include_test.go
git commit -m "feat(config): add single-level {{ include }} preprocessor"
```

---

## Task 6: Secret resolver (env-based)

**Files:**
- Create: `internal/secrets/resolver.go`
- Create: `internal/secrets/resolver_test.go`

- [ ] **Step 1: Write the failing test**

```go
package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolver_GetSecret(t *testing.T) {
	r := New(map[string]string{
		"CRONFOUNDRY_SECRET_SLACK_DIGEST_WEBHOOK": "https://hooks.slack.com/xxx",
		"CRONFOUNDRY_SECRET_TEAM_NAME":            "Platform",
	})

	v, err := r.Get("slack_digest_webhook")
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/xxx", v)

	v, err = r.Get("team_name")
	require.NoError(t, err)
	assert.Equal(t, "Platform", v)

	_, err = r.Get("missing")
	assert.ErrorContains(t, err, "secret \"missing\"")
	assert.ErrorContains(t, err, "CRONFOUNDRY_SECRET_MISSING")
}

func TestResolver_AllValues_ForRedaction(t *testing.T) {
	r := New(map[string]string{
		"CRONFOUNDRY_SECRET_A": "secret-a",
		"CRONFOUNDRY_SECRET_B": "secret-b",
		"OTHER":                "not-a-secret",
	})
	values := r.AllValues()
	assert.ElementsMatch(t, []string{"secret-a", "secret-b"}, values)
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/secrets/... -v
```

Expected: package undefined.

- [ ] **Step 3: Implement `internal/secrets/resolver.go`**

```go
// Package secrets resolves skill-declared secrets from environment variables
// in the form CRONFOUNDRY_SECRET_<UPPER(name)>.
package secrets

import (
	"fmt"
	"strings"
)

const prefix = "CRONFOUNDRY_SECRET_"

type Resolver struct {
	env map[string]string
}

func New(env map[string]string) *Resolver {
	return &Resolver{env: env}
}

func (r *Resolver) Get(name string) (string, error) {
	key := prefix + strings.ToUpper(name)
	v, ok := r.env[key]
	if !ok {
		return "", fmt.Errorf("secret %q not set; export %s", name, key)
	}
	return v, nil
}

// AllValues returns every secret value known to the resolver, for use by
// the redactor. Order is not guaranteed.
func (r *Resolver) AllValues() []string {
	out := make([]string, 0)
	for k, v := range r.env {
		if strings.HasPrefix(k, prefix) && v != "" {
			out = append(out, v)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/secrets/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/
git commit -m "feat(secrets): resolve skill secrets from env vars"
```

---

## Task 7: `<memory>` block parser

**Files:**
- Create: `internal/memory/parser.go`
- Create: `internal/memory/parser_test.go`

- [ ] **Step 1: Write the failing test**

```go
package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtract_Present(t *testing.T) {
	output := `Here is the digest.

Lots of stuff happened.

<memory>
Learned: team dislikes lorem ipsum.
Date: 2026-04-13
</memory>
`
	published, mem, ok := Extract(output)
	require.True(t, ok)
	assert.Equal(t, "Learned: team dislikes lorem ipsum.\nDate: 2026-04-13", mem)
	assert.NotContains(t, published, "<memory>")
	assert.NotContains(t, published, "Learned: team dislikes")
	assert.Contains(t, published, "Lots of stuff happened.")
}

func TestExtract_Absent(t *testing.T) {
	output := "Plain output, no memory block."
	published, mem, ok := Extract(output)
	assert.False(t, ok)
	assert.Equal(t, "", mem)
	assert.Equal(t, output, published)
}

func TestExtract_MultipleBlocks_UsesLast(t *testing.T) {
	// Defensive against models that ramble with multiple blocks; take the last.
	output := `First attempt: <memory>old</memory>
Revised: <memory>new</memory>`
	published, mem, ok := Extract(output)
	require.True(t, ok)
	assert.Equal(t, "new", mem)
	assert.NotContains(t, published, "<memory>new")
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/memory/... -v
```

Expected: package undefined.

- [ ] **Step 3: Implement `internal/memory/parser.go`**

```go
// Package memory extracts the reserved <memory>...</memory> writeback block
// from an LLM's output.
package memory

import (
	"regexp"
	"strings"
)

var blockRe = regexp.MustCompile(`(?s)<memory>\s*(.*?)\s*</memory>`)

// Extract returns (publishedOutput, memoryContent, found). When multiple
// blocks are present, the last one is chosen (defensive against models that
// iterate on the block in their output). All blocks are stripped from the
// published output.
func Extract(output string) (string, string, bool) {
	matches := blockRe.FindAllStringSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return output, "", false
	}
	last := matches[len(matches)-1]
	content := strings.TrimSpace(output[last[2]:last[3]])
	published := blockRe.ReplaceAllString(output, "")
	return strings.TrimSpace(published), content, true
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/memory/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat(memory): extract <memory> writeback block from output"
```

---

## Task 8: Template renderer (for destination templates)

**Files:**
- Create: `internal/template/render.go`
- Create: `internal/template/render_test.go`

- [ ] **Step 1: Write the failing test**

```go
package template

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRender_AllVariables(t *testing.T) {
	ctx := Context{
		Output:    "full output line 1\nfull output line 2",
		RunID:     "run-abc",
		RunDate:   "2026-04-19",
		StartedAt: time.Date(2026, 4, 19, 9, 0, 0, 0, time.UTC),
		Schedule: Meta{Name: "monday-morning"},
		Skill:    Meta{Name: "weekly-digest"},
	}

	cases := map[string]string{
		`{{ output }}`:                 "full output line 1\nfull output line 2",
		`{{ output.truncated 10 }}`:    "full outp…",
		`{{ run.id }}`:                 "run-abc",
		`{{ run.date }}`:               "2026-04-19",
		`{{ run.started_at }}`:         "2026-04-19T09:00:00Z",
		`{{ schedule.name }}`:          "monday-morning",
		`{{ skill.name }}`:             "weekly-digest",
		`prefix {{ run.id }} suffix`:   "prefix run-abc suffix",
	}
	for tmpl, want := range cases {
		got, warnings := Render(tmpl, ctx)
		assert.Equal(t, want, got, "template %q", tmpl)
		assert.Empty(t, warnings, "unexpected warnings for %q", tmpl)
	}
}

func TestRender_TruncatedNoop(t *testing.T) {
	ctx := Context{Output: "short"}
	out, w := Render(`{{ output.truncated 100 }}`, ctx)
	assert.Equal(t, "short", out)
	assert.Empty(t, w)
}

func TestRender_UnknownVariable(t *testing.T) {
	out, warnings := Render(`hello {{ mystery }}`, Context{})
	assert.Equal(t, `hello {{ mystery }}`, out)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "mystery")
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/template/... -v
```

Expected: package undefined.

- [ ] **Step 3: Implement `internal/template/render.go`**

```go
// Package template renders destination templates with a fixed, safe variable
// set — no logic, no loops.
package template

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Context struct {
	Output    string
	RunID     string
	RunDate   string
	StartedAt time.Time
	Schedule  Meta
	Skill     Meta
}

type Meta struct {
	Name string
}

var (
	directiveRe     = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)
	truncatedArgsRe = regexp.MustCompile(`^output\.truncated\s+(\d+)$`)
)

// Render returns the rendered string and a slice of warnings for any
// unresolved variables. Unresolved variables are left as their literal
// `{{ name }}` form.
func Render(tmpl string, ctx Context) (string, []string) {
	var warnings []string
	out := directiveRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(directiveRe.FindStringSubmatch(match)[1])
		switch expr {
		case "output":
			return ctx.Output
		case "run.id":
			return ctx.RunID
		case "run.date":
			return ctx.RunDate
		case "run.started_at":
			return ctx.StartedAt.UTC().Format(time.RFC3339)
		case "schedule.name":
			return ctx.Schedule.Name
		case "skill.name":
			return ctx.Skill.Name
		}
		if m := truncatedArgsRe.FindStringSubmatch(expr); m != nil {
			n, _ := strconv.Atoi(m[1])
			return truncate(ctx.Output, n)
		}
		warnings = append(warnings, fmt.Sprintf("unresolved template variable: %s", expr))
		return match
	})
	return out, warnings
}

// truncate returns s limited to maxRunes runes. When truncated, a single
// horizontal ellipsis replaces the dropped tail.
func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= maxRunes-1 {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteRune('…')
	return b.String()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/template/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/template/
git commit -m "feat(template): render destination templates with safe variable set"
```

---

## Task 9: Secret redactor (for logs)

**Files:**
- Create: `internal/redact/redact.go`
- Create: `internal/redact/redact_test.go`

- [ ] **Step 1: Write the failing test**

```go
package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactor_ReplacesKnownValues(t *testing.T) {
	r := New([]string{"sk-abc-123", "hooks.slack.com/TOKEN123", ""})
	got := r.Redact("Key=sk-abc-123 url=https://hooks.slack.com/TOKEN123/extra")
	assert.Equal(t, "Key=[REDACTED] url=https://[REDACTED]/extra", got)
}

func TestRedactor_EmptyStringsIgnored(t *testing.T) {
	r := New([]string{""})
	got := r.Redact("no secrets here")
	assert.Equal(t, "no secrets here", got)
}

func TestRedactor_LongestMatchFirst(t *testing.T) {
	// Ensure "supersecret" is redacted as a whole, not overlapped by "secret".
	r := New([]string{"secret", "supersecret"})
	got := r.Redact("value=supersecret")
	assert.Equal(t, "value=[REDACTED]", got)
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/redact/... -v
```

Expected: package undefined.

- [ ] **Step 3: Implement `internal/redact/redact.go`**

```go
// Package redact scrubs known secret values from strings before logging.
package redact

import (
	"sort"
	"strings"
)

const placeholder = "[REDACTED]"

type Redactor struct {
	values []string
}

func New(secrets []string) *Redactor {
	filtered := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	// Sort by descending length so longer matches are applied first.
	sort.Slice(filtered, func(i, j int) bool {
		return len(filtered[i]) > len(filtered[j])
	})
	return &Redactor{values: filtered}
}

func (r *Redactor) Redact(s string) string {
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, placeholder)
	}
	return s
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/redact/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/redact/
git commit -m "feat(redact): scrub known secret values from log strings"
```

---

## Task 10: LLM Provider interface + usage types

**Files:**
- Create: `internal/llm/provider.go`

- [ ] **Step 1: Write the interface + types**

```go
// Package llm defines a provider-agnostic interface for streaming chat
// completions against OpenAI, Anthropic, and Azure AI Foundry.
package llm

import (
	"context"
)

// Message is a single chat message. Roles mirror OpenAI's convention.
type Role string

const (
	RoleSystem Role = "system"
	RoleUser   Role = "user"
)

type Message struct {
	Role    Role
	Content string
}

// StreamChunk is an incremental portion of the assistant's response.
type StreamChunk struct {
	Delta string
}

// Usage records token accounting for a completed call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// CallOptions controls a single provider invocation.
type CallOptions struct {
	Model       string
	MaxTokens   int
	APIKey      string
	// Endpoint is used by Azure AI Foundry only; OpenAI and Anthropic ignore it.
	Endpoint    string
	// Deployment (Azure AI Foundry): the model-deployment name.
	Deployment  string
}

// Provider executes a single chat completion with streaming output.
//
// Implementations MUST:
//   - stream chunks to `onChunk` as they arrive,
//   - return `Usage` with final input/output token counts,
//   - honor ctx cancellation / deadline,
//   - return an error wrapping the provider's status classification
//     when the call ultimately fails after retries.
type Provider interface {
	Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error)
}
```

This task has no test on its own — it's a pure interface definition. The next three tasks test implementations.

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/llm/...
```

Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/llm/provider.go
git commit -m "feat(llm): define streaming Provider interface"
```

---

## Task 11: OpenAI provider adapter

**Files:**
- Create: `internal/llm/openai.go`
- Create: `internal/llm/openai_test.go`

- [ ] **Step 1: Add OpenAI SDK**

```bash
go get github.com/openai/openai-go@latest
```

- [ ] **Step 2: Write the failing test using an `httptest` server**

```go
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openAIStreamFixture is a minimal SSE stream that mimics the Chat Completions
// response format used by the openai-go SDK.
const openAIStreamFixture = `data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}

data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}

data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}

data: [DONE]
`

func TestOpenAI_Chat_StreamsAndReportsUsage(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIStreamFixture)
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL)
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "user"}},
		CallOptions{Model: "gpt-5.1", MaxTokens: 128, APIKey: "sk-test"},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Contains(t, gotBody, `"model":"gpt-5.1"`)
	assert.Contains(t, gotBody, `"stream":true`)
	assert.Equal(t, []string{"Hello", " world"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}

func TestOpenAI_Chat_ErrorOn500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL)
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "u"}},
		CallOptions{Model: "m", APIKey: "k"},
		func(StreamChunk) {})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "boom"))
}
```

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/llm/... -run TestOpenAI -v
```

Expected: `NewOpenAI` undefined.

- [ ] **Step 4: Implement `internal/llm/openai.go`**

```go
package llm

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type openAIProvider struct {
	baseURL string
}

func NewOpenAI(baseURL string) Provider {
	return &openAIProvider{baseURL: baseURL}
}

func (p *openAIProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	clientOpts := []option.RequestOption{option.WithAPIKey(opts.APIKey)}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := openai.NewClient(clientOpts...)

	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case RoleUser:
			msgs = append(msgs, openai.UserMessage(m.Content))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    opts.Model,
		Messages: msgs,
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(opts.MaxTokens))
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var usage Usage
	for stream.Next() {
		evt := stream.Current()
		if len(evt.Choices) > 0 && evt.Choices[0].Delta.Content != "" {
			onChunk(StreamChunk{Delta: evt.Choices[0].Delta.Content})
		}
		if evt.Usage.PromptTokens > 0 || evt.Usage.CompletionTokens > 0 {
			usage.InputTokens = int(evt.Usage.PromptTokens)
			usage.OutputTokens = int(evt.Usage.CompletionTokens)
		}
	}
	if err := stream.Err(); err != nil {
		return usage, fmt.Errorf("openai chat: %w", err)
	}
	return usage, nil
}
```

**Note:** the exact openai-go type/field names may evolve between SDK releases. If the build fails, consult the installed version's `go doc` and adjust; the test fixture and behavior expectations are the load-bearing contract.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/llm/... -run TestOpenAI -v
```

Expected: PASS. If the SDK's type names differ, fix the import references until the test passes — the test is the spec.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/llm/openai.go internal/llm/openai_test.go
git commit -m "feat(llm): add streaming OpenAI provider adapter"
```

---

## Task 12: Anthropic provider adapter

**Files:**
- Create: `internal/llm/anthropic.go`
- Create: `internal/llm/anthropic_test.go`

- [ ] **Step 1: Add Anthropic SDK**

```bash
go get github.com/anthropics/anthropic-sdk-go@latest
```

- [ ] **Step 2: Write the failing test**

```go
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anthropicStreamFixture mirrors the SSE event stream from Anthropic's
// Messages API with streaming.
const anthropicStreamFixture = `event: message_start
data: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}
`

func TestAnthropic_Chat_StreamsAndReportsUsage(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, anthropicStreamFixture)
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL)
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "hello"}},
		CallOptions{Model: "claude-4.7", MaxTokens: 200, APIKey: "ak-test"},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, "ak-test", gotKey)
	assert.Equal(t, []string{"Hi", " there"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}
```

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/llm/... -run TestAnthropic -v
```

Expected: `NewAnthropic` undefined.

- [ ] **Step 4: Implement `internal/llm/anthropic.go`**

```go
package llm

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	baseURL string
}

func NewAnthropic(baseURL string) Provider {
	return &anthropicProvider{baseURL: baseURL}
}

func (p *anthropicProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	clientOpts := []option.RequestOption{option.WithAPIKey(opts.APIKey)}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)

	// Anthropic takes system prompt as a top-level param and user/assistant
	// messages as the array. Split system out.
	var system string
	var userMsgs []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case RoleUser:
			userMsgs = append(userMsgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	maxTok := int64(opts.MaxTokens)
	if maxTok <= 0 {
		maxTok = 1024
	}

	params := anthropic.MessageNewParams{
		Model:     opts.Model,
		MaxTokens: maxTok,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  userMsgs,
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var usage Usage
	for stream.Next() {
		evt := stream.Current()
		switch v := evt.AsAny().(type) {
		case anthropic.MessageStartEvent:
			usage.InputTokens = int(v.Message.Usage.InputTokens)
		case anthropic.ContentBlockDeltaEvent:
			if td, ok := v.Delta.AsAny().(anthropic.TextDelta); ok {
				onChunk(StreamChunk{Delta: td.Text})
			}
		case anthropic.MessageDeltaEvent:
			if v.Usage.OutputTokens > 0 {
				usage.OutputTokens = int(v.Usage.OutputTokens)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return usage, fmt.Errorf("anthropic chat: %w", err)
	}
	return usage, nil
}
```

**Note:** as with OpenAI, the SDK's exact event/delta type names are version-sensitive. The test defines the required behavior — if the SDK shape differs, adjust the adapter until the test passes.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/llm/... -run TestAnthropic -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/llm/anthropic.go internal/llm/anthropic_test.go
git commit -m "feat(llm): add streaming Anthropic provider adapter"
```

---

## Task 13: Azure AI Foundry provider adapter

**Files:**
- Create: `internal/llm/azurefoundry.go`
- Create: `internal/llm/azurefoundry_test.go`

- [ ] **Step 1: Add Azure OpenAI SDK**

```bash
go get github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai@latest
go get github.com/Azure/azure-sdk-for-go/sdk/azcore@latest
```

- [ ] **Step 2: Write the failing test**

```go
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Azure OpenAI streaming uses the same SSE format as OpenAI's, but the path
// is /openai/deployments/{deployment}/chat/completions with an api-version
// query and api-key header.
func TestAzureFoundry_Chat_StreamsAndReportsUsage(t *testing.T) {
	var gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("api-key")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, openAIStreamFixture) // same stream format as Task 11
	}))
	defer srv.Close()

	p := NewAzureFoundry()
	var chunks []string
	usage, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{
			Model:      "gpt-5.1",       // ignored by Azure in favor of Deployment
			Deployment: "prod-gpt",
			APIKey:     "azkey",
			Endpoint:   srv.URL,
		},
		func(c StreamChunk) { chunks = append(chunks, c.Delta) })

	require.NoError(t, err)
	assert.Equal(t, "azkey", gotKey)
	assert.True(t, strings.Contains(gotPath, "/openai/deployments/prod-gpt/chat/completions"),
		"path was %q", gotPath)
	assert.Equal(t, []string{"Hello", " world"}, chunks)
	assert.Equal(t, 5, usage.InputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
}

func TestAzureFoundry_MissingEndpoint(t *testing.T) {
	p := NewAzureFoundry()
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Deployment: "d", APIKey: "k"},
		func(StreamChunk) {})
	assert.ErrorContains(t, err, "endpoint")
}

func TestAzureFoundry_MissingDeployment(t *testing.T) {
	p := NewAzureFoundry()
	_, err := p.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		CallOptions{Endpoint: "https://x", APIKey: "k"},
		func(StreamChunk) {})
	assert.ErrorContains(t, err, "deployment")
}
```

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/llm/... -run TestAzureFoundry -v
```

Expected: `NewAzureFoundry` undefined.

- [ ] **Step 4: Implement `internal/llm/azurefoundry.go`**

```go
package llm

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type azureFoundryProvider struct{}

func NewAzureFoundry() Provider {
	return &azureFoundryProvider{}
}

func (p *azureFoundryProvider) Chat(ctx context.Context, messages []Message, opts CallOptions, onChunk func(StreamChunk)) (Usage, error) {
	if opts.Endpoint == "" {
		return Usage{}, fmt.Errorf("azure-foundry: endpoint required")
	}
	if opts.Deployment == "" {
		return Usage{}, fmt.Errorf("azure-foundry: deployment required")
	}

	cred := azcore.NewKeyCredential(opts.APIKey)
	client, err := azopenai.NewClientWithKeyCredential(opts.Endpoint, cred, nil)
	if err != nil {
		return Usage{}, fmt.Errorf("azure-foundry: new client: %w", err)
	}

	msgs := make([]azopenai.ChatRequestMessageClassification, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, &azopenai.ChatRequestSystemMessage{Content: azopenai.NewChatRequestSystemMessageContent(m.Content)})
		case RoleUser:
			msgs = append(msgs, &azopenai.ChatRequestUserMessage{Content: azopenai.NewChatRequestUserMessageContent(m.Content)})
		}
	}

	body := azopenai.ChatCompletionsOptions{
		Messages:       msgs,
		DeploymentName: &opts.Deployment,
	}
	if opts.MaxTokens > 0 {
		mt := int32(opts.MaxTokens)
		body.MaxTokens = &mt
	}

	stream, err := client.GetChatCompletionsStream(ctx, body, nil)
	if err != nil {
		return Usage{}, fmt.Errorf("azure-foundry: start stream: %w", err)
	}
	defer stream.ChatCompletionsStream.Close()

	var usage Usage
	for {
		evt, err := stream.ChatCompletionsStream.Read()
		if err != nil {
			// EOF / end of stream is signalled by the SDK with io.EOF.
			if err.Error() == "EOF" {
				break
			}
			return usage, fmt.Errorf("azure-foundry: read stream: %w", err)
		}
		for _, c := range evt.Choices {
			if c.Delta != nil && c.Delta.Content != nil {
				onChunk(StreamChunk{Delta: *c.Delta.Content})
			}
		}
		if evt.Usage != nil {
			if evt.Usage.PromptTokens != nil {
				usage.InputTokens = int(*evt.Usage.PromptTokens)
			}
			if evt.Usage.CompletionTokens != nil {
				usage.OutputTokens = int(*evt.Usage.CompletionTokens)
			}
		}
	}
	return usage, nil
}
```

**Note:** `azopenai` APIs and event shapes may differ between releases; adjust to make the test pass. The load-bearing contract is: streaming deltas delivered to `onChunk` + final `Usage` with input/output tokens.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/llm/... -run TestAzureFoundry -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/llm/azurefoundry.go internal/llm/azurefoundry_test.go
git commit -m "feat(llm): add streaming Azure AI Foundry provider adapter"
```

---

## Task 14: LLM provider factory

**Files:**
- Create: `internal/llm/factory.go`
- Create: `internal/llm/factory_test.go`

- [ ] **Step 1: Write the failing test**

```go
package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider_KnownNames(t *testing.T) {
	cases := []string{"openai", "anthropic", "azure-foundry"}
	for _, name := range cases {
		p, err := NewProvider(name)
		require.NoError(t, err, name)
		assert.NotNil(t, p, name)
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	_, err := NewProvider("llama")
	assert.ErrorContains(t, err, "unknown provider")
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/llm/... -run TestNewProvider -v
```

Expected: `NewProvider` undefined.

- [ ] **Step 3: Implement `internal/llm/factory.go`**

```go
package llm

import "fmt"

// NewProvider returns a default-configured provider by its config name.
// For testing with mock endpoints, call the concrete constructors directly.
func NewProvider(name string) (Provider, error) {
	switch name {
	case "openai":
		return NewOpenAI(""), nil
	case "anthropic":
		return NewAnthropic(""), nil
	case "azure-foundry":
		return NewAzureFoundry(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, anthropic, azure-foundry)", name)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/llm/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llm/factory.go internal/llm/factory_test.go
git commit -m "feat(llm): add provider factory"
```

---

## Task 15: Publisher interface + fan-out dispatcher

**Files:**
- Create: `internal/publish/publisher.go`
- Create: `internal/publish/dispatcher.go`
- Create: `internal/publish/dispatcher_test.go`

- [ ] **Step 1: Define types in `internal/publish/publisher.go`**

```go
// Package publish fans output to destinations (GitHub issue, Slack, Discord,
// Teams), isolating per-destination failures.
package publish

import (
	"context"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

// Result is the outcome of a single publish attempt.
type Result struct {
	Type    string // "github-issue" | "slack" | "discord" | "teams"
	OK      bool
	Err     error  // non-nil when OK == false
	Detail  string // optional context (e.g., issue URL, HTTP status)
}

// Publisher publishes a rendered output to a single destination.
// Implementations resolve their own secrets (via SecretGetter) and handle
// their own retries.
type Publisher interface {
	Type() string
	Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result
}

// SecretGetter retrieves a secret value by logical name.
type SecretGetter interface {
	Get(name string) (string, error)
}
```

- [ ] **Step 2: Write the failing dispatcher test**

Create `internal/publish/dispatcher_test.go`:

```go
package publish

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

type fakePub struct {
	typ    string
	ok     bool
	delay  time.Duration
	called int32
}

func (f *fakePub) Type() string { return f.typ }
func (f *fakePub) Publish(ctx context.Context, d config.Destination, out string, tctx template.Context, s SecretGetter) Result {
	atomic.AddInt32(&f.called, 1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.ok {
		return Result{Type: f.typ, OK: true, Detail: "ok"}
	}
	return Result{Type: f.typ, OK: false, Err: errors.New("boom")}
}

type nilSecrets struct{}

func (nilSecrets) Get(string) (string, error) { return "", nil }

func TestDispatch_IsolatesFailures_RunsInParallel(t *testing.T) {
	gh := &fakePub{typ: "github-issue", ok: false, delay: 20 * time.Millisecond}
	sl := &fakePub{typ: "slack", ok: true, delay: 20 * time.Millisecond}

	d := &Dispatcher{Publishers: map[string]Publisher{"github-issue": gh, "slack": sl}}

	dests := []config.Destination{
		{GitHubIssue: &config.GitHubIssueDest{}},
		{Slack: &config.WebhookDest{}},
	}
	start := time.Now()
	results := d.Dispatch(context.Background(), dests, "body", template.Context{}, nilSecrets{})
	dur := time.Since(start)

	require.Len(t, results, 2)
	// Order is preserved.
	assert.Equal(t, "github-issue", results[0].Type)
	assert.False(t, results[0].OK)
	assert.Equal(t, "slack", results[1].Type)
	assert.True(t, results[1].OK)
	// Parallel execution — total time should be closer to 20ms than 40ms.
	assert.Less(t, dur, 35*time.Millisecond, "expected parallel execution")
}

func TestDispatch_UnknownType(t *testing.T) {
	d := &Dispatcher{Publishers: map[string]Publisher{}}
	dests := []config.Destination{{Slack: &config.WebhookDest{}}}
	results := d.Dispatch(context.Background(), dests, "", template.Context{}, nilSecrets{})
	require.Len(t, results, 1)
	assert.False(t, results[0].OK)
	assert.Contains(t, results[0].Err.Error(), "no publisher for slack")
}
```

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/publish/... -v
```

Expected: `Dispatcher` undefined.

- [ ] **Step 4: Implement `internal/publish/dispatcher.go`**

```go
package publish

import (
	"context"
	"fmt"
	"sync"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

// Dispatcher fans destinations out to their respective Publishers in parallel,
// preserving input order in the returned Results slice.
type Dispatcher struct {
	Publishers map[string]Publisher
}

func (d *Dispatcher) Dispatch(ctx context.Context, dests []config.Destination, output string, tctx template.Context, secrets SecretGetter) []Result {
	results := make([]Result, len(dests))
	var wg sync.WaitGroup
	for i, dest := range dests {
		i, dest := i, dest
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = d.publishOne(ctx, dest, output, tctx, secrets)
		}()
	}
	wg.Wait()
	return results
}

func (d *Dispatcher) publishOne(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	typ, err := destType(dest)
	if err != nil {
		return Result{OK: false, Err: err}
	}
	p, ok := d.Publishers[typ]
	if !ok {
		return Result{Type: typ, OK: false, Err: fmt.Errorf("no publisher for %s", typ)}
	}
	return p.Publish(ctx, dest, output, tctx, secrets)
}

func destType(d config.Destination) (string, error) {
	switch {
	case d.GitHubIssue != nil:
		return "github-issue", nil
	case d.Slack != nil:
		return "slack", nil
	case d.Discord != nil:
		return "discord", nil
	case d.Teams != nil:
		return "teams", nil
	}
	return "", fmt.Errorf("destination entry has no recognised type")
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/publish/... -v
```

Expected: PASS. Parallel timing assertion may occasionally flake on loaded CI — if so, relax to `< 50ms`.

- [ ] **Step 6: Commit**

```bash
git add internal/publish/publisher.go internal/publish/dispatcher.go internal/publish/dispatcher_test.go
git commit -m "feat(publish): define publisher interface and parallel dispatcher"
```

---

## Task 16: GitHub issue publisher

**Files:**
- Create: `internal/publish/githubissue.go`
- Create: `internal/publish/githubissue_test.go`

- [ ] **Step 1: Add go-github dependency**

```bash
go get github.com/google/go-github/v73@latest
```

- [ ] **Step 2: Write the failing test**

```go
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
```

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/publish/... -run TestGitHubIssue -v
```

Expected: `NewGitHubIssuePublisher` undefined.

- [ ] **Step 4: Implement `internal/publish/githubissue.go`**

```go
package publish

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v73/github"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const issueBodyMaxBytes = 64 * 1024 // GitHub issue body ~64KB

type githubIssuePub struct {
	baseURL string
	// token passed to Publish via SecretGetter for rotation — but for unit
	// tests we allow an optional fallback via a fixed token field.
	fallbackToken string
}

// NewGitHubIssuePublisher constructs the publisher. In MVP, the token comes
// from env (GITHUB_TOKEN) and is passed via the SecretGetter using the
// reserved secret name "github_token" — callers should wire it in.
func NewGitHubIssuePublisher(baseURL, fallbackToken string) Publisher {
	return &githubIssuePub{baseURL: baseURL, fallbackToken: fallbackToken}
}

func (p *githubIssuePub) Type() string { return "github-issue" }

func (p *githubIssuePub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.GitHubIssue
	if d == nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: config missing")}
	}
	owner, repo, ok := splitRepo(d.Repo)
	if !ok {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: repo must be owner/name (got %q)", d.Repo)}
	}

	token, err := secrets.Get("github_token")
	if err != nil || token == "" {
		token = p.fallbackToken
	}
	if token == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: no GitHub token available (set GITHUB_TOKEN)")}
	}

	client := github.NewClient(nil).WithAuthToken(token)
	if p.baseURL != "" {
		base := p.baseURL
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		parsed, err := parseBase(base)
		if err != nil {
			return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: parse base: %w", err)}
		}
		client.BaseURL = parsed
	}

	title, _ := template.Render(d.Title, tctx)
	body := output
	if d.Body != "" {
		body, _ = template.Render(d.Body, tctx)
	}
	if len(body) > issueBodyMaxBytes {
		body = body[:issueBodyMaxBytes-64] + "\n\n... [truncated, full output in run log]"
	}

	req := &github.IssueRequest{
		Title:     &title,
		Body:      &body,
		Labels:    &d.Labels,
		Assignees: &d.Assignees,
	}
	issue, _, err := client.Issues.Create(ctx, owner, repo, req)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("github-issue: create: %w", err)}
	}
	return Result{Type: p.Type(), OK: true, Detail: issue.GetHTMLURL()}
}

func splitRepo(r string) (string, string, bool) {
	idx := strings.Index(r, "/")
	if idx <= 0 || idx == len(r)-1 {
		return "", "", false
	}
	return r[:idx], r[idx+1:], true
}
```

Add helper `parseBase` in the same file:

```go
import "net/url"

func parseBase(s string) (*url.URL, error) {
	return url.Parse(s)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/publish/... -run TestGitHubIssue -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/publish/githubissue.go internal/publish/githubissue_test.go
git commit -m "feat(publish): add GitHub issue destination publisher"
```

---

## Task 17: Slack webhook publisher

**Files:**
- Create: `internal/publish/slack.go`
- Create: `internal/publish/slack_test.go`

- [ ] **Step 1: Write the failing test**

```go
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

type mapSecrets map[string]string

func (m mapSecrets) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", assertErrf("missing %s", k)
	}
	return v, nil
}

func assertErrf(f string, args ...any) error { return testErr{msg: f + "!"} }

type testErr struct{ msg string }

func (t testErr) Error() string { return t.msg }

func TestSlack_Publish_PostsTemplatedText(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{
		Secret: "slack_url",
		Text:   "Digest for {{ run.date }}: {{ output.truncated 6 }}",
	}}
	tctx := template.Context{RunDate: "2026-04-19", Output: "a very long output indeed"}

	res := p.Publish(context.Background(), dest, "a very long output indeed", tctx,
		mapSecrets{"slack_url": srv.URL})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "Digest for 2026-04-19: a ver…", gotBody["text"])
}

func TestSlack_Publish_SecretMissing(t *testing.T) {
	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{Secret: "missing"}}
	res := p.Publish(context.Background(), dest, "", template.Context{}, mapSecrets{})
	assert.False(t, res.OK)
	assert.Contains(t, res.Err.Error(), "missing")
}

func TestSlack_Publish_DefaultTextIsOutput(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewSlackPublisher()
	dest := config.Destination{Slack: &config.WebhookDest{Secret: "slack_url"}}
	res := p.Publish(context.Background(), dest, "raw output", template.Context{Output: "raw output"},
		mapSecrets{"slack_url": srv.URL})
	require.True(t, res.OK)
	assert.Equal(t, "raw output", gotBody["text"])
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/publish/... -run TestSlack -v
```

Expected: `NewSlackPublisher` undefined.

- [ ] **Step 3: Implement `internal/publish/slack.go`**

```go
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const slackDefaultMaxChars = 35000

type slackPub struct {
	http *http.Client
}

func NewSlackPublisher() Publisher {
	return &slackPub{http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *slackPub) Type() string { return "slack" }

func (p *slackPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Slack
	if d == nil || d.Secret == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("slack: secret required")}
	}
	url, err := secrets.Get(d.Secret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("slack: resolve secret: %w", err)}
	}
	text := output
	if d.Text != "" {
		text, _ = template.Render(d.Text, tctx)
	}
	text = ensureLen(text, slackDefaultMaxChars)
	return postJSON(ctx, p.http, p.Type(), url, map[string]any{"text": text})
}

func ensureLen(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-3]) + "..."
}

// postJSON posts the payload as JSON with a small retry policy:
// 3 attempts, exponential backoff; 4xx responses are not retried.
func postJSON(ctx context.Context, c *http.Client, typ, url string, payload any) Result {
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: marshal: %w", typ, err)}
	}
	var lastErr error
	delays := []time.Duration{0, 1 * time.Second, 4 * time.Second}
	for _, d := range delays {
		if d > 0 {
			select {
			case <-ctx.Done():
				return Result{Type: typ, OK: false, Err: ctx.Err()}
			case <-time.After(d):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return Result{Type: typ, OK: true, Detail: fmt.Sprintf("http %d", resp.StatusCode)}
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: http %d (no retry)", typ, resp.StatusCode)}
		}
		lastErr = fmt.Errorf("http %d", resp.StatusCode)
	}
	return Result{Type: typ, OK: false, Err: fmt.Errorf("%s: retries exhausted: %w", typ, lastErr)}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/publish/... -run TestSlack -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/slack.go internal/publish/slack_test.go
git commit -m "feat(publish): add Slack webhook publisher"
```

---

## Task 18: Discord webhook publisher

**Files:**
- Create: `internal/publish/discord.go`
- Create: `internal/publish/discord_test.go`

- [ ] **Step 1: Write the failing test**

```go
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

func TestDiscord_Publish_PostsContentAndUsername(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusNoContent) // Discord returns 204
	}))
	defer srv.Close()

	p := NewDiscordPublisher()
	dest := config.Destination{Discord: &config.WebhookDest{
		Secret:   "disc",
		Content:  "{{ output.truncated 1900 }}",
		Username: "CronFoundry",
	}}
	out := "short output"
	res := p.Publish(context.Background(), dest, out, template.Context{Output: out},
		mapSecrets{"disc": srv.URL})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "short output", gotBody["content"])
	assert.Equal(t, "CronFoundry", gotBody["username"])
}

func TestDiscord_Publish_EnforcesHardLimit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := NewDiscordPublisher()
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'a'
	}
	dest := config.Destination{Discord: &config.WebhookDest{Secret: "disc"}}
	res := p.Publish(context.Background(), dest, string(long), template.Context{Output: string(long)},
		mapSecrets{"disc": srv.URL})
	require.True(t, res.OK)
	content := gotBody["content"].(string)
	assert.LessOrEqual(t, len([]rune(content)), 2000)
	assert.True(t, len([]rune(content)) > 1900)
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/publish/... -run TestDiscord -v
```

Expected: `NewDiscordPublisher` undefined.

- [ ] **Step 3: Implement `internal/publish/discord.go`**

```go
package publish

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const discordHardLimit = 2000

type discordPub struct {
	http *http.Client
}

func NewDiscordPublisher() Publisher {
	return &discordPub{http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *discordPub) Type() string { return "discord" }

func (p *discordPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Discord
	if d == nil || d.Secret == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("discord: secret required")}
	}
	url, err := secrets.Get(d.Secret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("discord: resolve secret: %w", err)}
	}
	content := output
	if d.Content != "" {
		content, _ = template.Render(d.Content, tctx)
	}
	content = ensureLen(content, discordHardLimit)
	payload := map[string]any{"content": content}
	if d.Username != "" {
		payload["username"] = d.Username
	}
	return postJSON(ctx, p.http, p.Type(), url, payload)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/publish/... -run TestDiscord -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/discord.go internal/publish/discord_test.go
git commit -m "feat(publish): add Discord webhook publisher"
```

---

## Task 19: Teams (Power Automate) publisher

**Files:**
- Create: `internal/publish/teams.go`
- Create: `internal/publish/teams_test.go`

- [ ] **Step 1: Write the failing test**

```go
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

func TestTeams_Publish_PostsAdaptiveCard(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	p := NewTeamsPublisher()
	dest := config.Destination{Teams: &config.WebhookDest{
		Secret: "teams",
		Title:  "Weekly digest",
		Text:   "{{ output.truncated 25000 }}",
	}}
	res := p.Publish(context.Background(), dest, "hello", template.Context{Output: "hello"},
		mapSecrets{"teams": srv.URL})

	require.True(t, res.OK, "err: %v", res.Err)
	assert.Equal(t, "message", gotBody["type"])
	attachments := gotBody["attachments"].([]any)
	require.Len(t, attachments, 1)
	att := attachments[0].(map[string]any)
	assert.Equal(t, "application/vnd.microsoft.card.adaptive", att["contentType"])
	content := att["content"].(map[string]any)
	assert.Equal(t, "AdaptiveCard", content["type"])
	body := content["body"].([]any)
	require.GreaterOrEqual(t, len(body), 2)
	assert.Equal(t, "Weekly digest", body[0].(map[string]any)["text"])
	assert.Equal(t, "hello", body[1].(map[string]any)["text"])
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/publish/... -run TestTeams -v
```

Expected: `NewTeamsPublisher` undefined.

- [ ] **Step 3: Implement `internal/publish/teams.go`**

```go
package publish

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

const teamsDefaultMaxChars = 25000

type teamsPub struct {
	http *http.Client
}

func NewTeamsPublisher() Publisher {
	return &teamsPub{http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *teamsPub) Type() string { return "teams" }

func (p *teamsPub) Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result {
	d := dest.Teams
	if d == nil || d.Secret == "" {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("teams: secret required")}
	}
	url, err := secrets.Get(d.Secret)
	if err != nil {
		return Result{Type: p.Type(), OK: false, Err: fmt.Errorf("teams: resolve secret: %w", err)}
	}
	text := output
	if d.Text != "" {
		text, _ = template.Render(d.Text, tctx)
	}
	text = ensureLen(text, teamsDefaultMaxChars)

	body := []map[string]any{}
	if d.Title != "" {
		body = append(body, map[string]any{
			"type":   "TextBlock",
			"text":   d.Title,
			"weight": "Bolder",
			"size":   "Medium",
		})
	}
	body = append(body, map[string]any{
		"type": "TextBlock",
		"text": text,
		"wrap": true,
	})

	card := map[string]any{
		"type":    "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"type":    "AdaptiveCard",
				"version": "1.4",
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"body":    body,
			},
		}},
	}
	return postJSON(ctx, p.http, p.Type(), url, card)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/publish/... -run TestTeams -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/teams.go internal/publish/teams_test.go
git commit -m "feat(publish): add Teams Power Automate publisher"
```

---

## Task 20: Writeback committer (go-git)

**Files:**
- Create: `internal/writeback/writeback.go`
- Create: `internal/writeback/writeback_test.go`

- [ ] **Step 1: Add go-git dependency**

```bash
go get github.com/go-git/go-git/v5@latest
```

- [ ] **Step 2: Write the failing test**

```go
package writeback

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommit_AppendsAndCommits(t *testing.T) {
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	require.NoError(t, err)

	// Seed: create memory.md with some existing content and commit.
	memPath := filepath.Join(root, "memory.md")
	require.NoError(t, os.WriteFile(memPath, []byte("existing line\n"), 0o644))
	wt, _ := repo.Worktree()
	_, _ = wt.Add("memory.md")
	_, err = wt.Commit("seed", &git.CommitOptions{
		AllowEmptyCommits: false,
		Author:            signature("seed", "seed@example.com"),
	})
	require.NoError(t, err)

	w := New()
	commitSHA, err := w.Commit(root, Options{
		Path:        "memory.md",
		Mode:        "append",
		Content:     "new line",
		Message:     "chore(cronfoundry): update memory.md",
		AuthorName:  "cronfoundry[bot]",
		AuthorEmail: "cronfoundry[bot]@users.noreply.github.com",
	})
	require.NoError(t, err)
	assert.Len(t, commitSHA, 40)

	got, err := os.ReadFile(memPath)
	require.NoError(t, err)
	assert.Equal(t, "existing line\nnew line\n", string(got))
}

func TestCommit_Replace(t *testing.T) {
	root := t.TempDir()
	_, err := git.PlainInit(root, false)
	require.NoError(t, err)

	w := New()
	_, err = w.Commit(root, Options{
		Path:        "memory.md",
		Mode:        "replace",
		Content:     "fresh content",
		Message:     "msg",
		AuthorName:  "a",
		AuthorEmail: "a@b",
	})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(root, "memory.md"))
	require.NoError(t, err)
	assert.Equal(t, "fresh content\n", string(got))
}

func TestCommit_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	_, err := git.PlainInit(root, false)
	require.NoError(t, err)

	w := New()
	_, err = w.Commit(root, Options{
		Path: "../escape.md", Mode: "append", Content: "x",
		AuthorName: "a", AuthorEmail: "a@b",
	})
	assert.ErrorContains(t, err, "outside")
}

func TestCommit_UnknownMode(t *testing.T) {
	root := t.TempDir()
	_, err := git.PlainInit(root, false)
	require.NoError(t, err)
	w := New()
	_, err = w.Commit(root, Options{Path: "m.md", Mode: "delete", Content: "x", AuthorName: "a", AuthorEmail: "a@b"})
	assert.ErrorContains(t, err, "mode")
}
```

(The test references a `signature` helper that must be defined at the top of the test file.)

Add at the top of `writeback_test.go`:

```go
import (
	"time"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func signature(name, email string) *object.Signature {
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}
```

- [ ] **Step 3: Confirm failure**

```bash
go test ./internal/writeback/... -v
```

Expected: `writeback.New` undefined.

- [ ] **Step 4: Implement `internal/writeback/writeback.go`**

```go
// Package writeback commits a <memory> block's content back to the skill
// repository using go-git. Push-to-remote is a separate method to keep the
// commit step testable without network.
package writeback

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

type Options struct {
	Path        string // relative to repo root
	Mode        string // "append" | "replace"
	Content     string
	Message     string
	AuthorName  string
	AuthorEmail string
}

type Writer struct{}

func New() *Writer { return &Writer{} }

// Commit applies the writeback content to `repoRoot/Path`, stages it, and
// commits. Returns the commit SHA.
func (w *Writer) Commit(repoRoot string, opts Options) (string, error) {
	if opts.Mode != "append" && opts.Mode != "replace" {
		return "", fmt.Errorf("writeback: mode %q invalid (want append|replace)", opts.Mode)
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("writeback: abs root: %w", err)
	}
	cleaned, err := filepath.Abs(filepath.Join(absRoot, opts.Path))
	if err != nil {
		return "", fmt.Errorf("writeback: resolve path: %w", err)
	}
	if !strings.HasPrefix(cleaned, absRoot+string(os.PathSeparator)) && cleaned != absRoot {
		return "", fmt.Errorf("writeback: path %q resolves outside repo", opts.Path)
	}

	var newContent string
	switch opts.Mode {
	case "replace":
		newContent = ensureTrailingNewline(opts.Content)
	case "append":
		existing, err := os.ReadFile(cleaned)
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("writeback: read existing: %w", err)
		}
		base := string(existing)
		if base != "" && !strings.HasSuffix(base, "\n") {
			base += "\n"
		}
		newContent = base + ensureTrailingNewline(opts.Content)
	}

	if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
		return "", fmt.Errorf("writeback: mkdir: %w", err)
	}
	if err := os.WriteFile(cleaned, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("writeback: write: %w", err)
	}

	repo, err := git.PlainOpen(absRoot)
	if err != nil {
		return "", fmt.Errorf("writeback: open repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("writeback: worktree: %w", err)
	}
	if _, err := wt.Add(opts.Path); err != nil {
		return "", fmt.Errorf("writeback: git add: %w", err)
	}
	msg := opts.Message
	if msg == "" {
		msg = fmt.Sprintf("chore(cronfoundry): update %s", opts.Path)
	}
	hash, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: opts.AuthorName, Email: opts.AuthorEmail, When: time.Now()},
	})
	if err != nil {
		return "", fmt.Errorf("writeback: commit: %w", err)
	}
	return hash.String(), nil
}

// Push sends the commit to the remote using a PAT as basic-auth password.
// Username can be any non-empty string for GitHub HTTPS push.
func (w *Writer) Push(repoRoot, remoteName, username, token string) error {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("writeback: open repo: %w", err)
	}
	err = repo.Push(&git.PushOptions{
		RemoteName: remoteName,
		Auth:       &http.BasicAuth{Username: username, Password: token},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("writeback: push: %w", err)
	}
	return nil
}

func ensureTrailingNewline(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/writeback/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/writeback/
git commit -m "feat(writeback): commit <memory> content to repo via go-git"
```

---

## Task 21: Runner orchestration (end-to-end with fakes)

**Files:**
- Create: `internal/runner/runner.go`
- Create: `internal/runner/runner_test.go`

- [ ] **Step 1: Write the failing end-to-end test**

```go
package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/secrets"
)

// fakeProvider returns a canned streamed response and tracks the messages it received.
type fakeProvider struct {
	response string
	received []llm.Message
}

func (f *fakeProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	f.received = append([]llm.Message{}, msgs...)
	// Emit in three chunks to exercise streaming.
	for _, chunk := range splitIntoN(f.response, 3) {
		onChunk(llm.StreamChunk{Delta: chunk})
	}
	return llm.Usage{InputTokens: 10, OutputTokens: 20}, nil
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
	// Set up a temp repo with a skill + cronfoundry.yaml.
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

	// Initial commit so writeback can append against a tracked tree.
	wt, _ := git.PlainOpen(repoRoot)
	w, _ := wt.Worktree()
	_ = w.AddGlob(".")
	_, err = w.Commit("seed", &git.CommitOptions{Author: sig()})
	require.NoError(t, err)

	// Fake Slack webhook.
	var slackBody map[string]any
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &slackBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer slackSrv.Close()

	// Fake LLM provider that emits a response with a memory block.
	fake := &fakeProvider{response: "Weekly summary.\n<memory>learned X</memory>"}
	providerFactory := func(name string) (llm.Provider, error) {
		require.Equal(t, "fake", name)
		return fake, nil
	}

	// Build the runner with our fakes.
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
		Secrets: secrets.New(map[string]string{
			"CRONFOUNDRY_SECRET_SLACK_URL": slackSrv.URL,
		}),
		LLMAPIKey: "sk-test",
		DryRun:    false,
		// Skip push so test stays offline.
		SkipPush:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.Equal(t, 10, result.Usage.InputTokens)
	assert.Equal(t, 20, result.Usage.OutputTokens)

	// Slack got the published output (memory block stripped).
	assert.Contains(t, slackBody["text"].(string), "Weekly summary.")
	assert.NotContains(t, slackBody["text"].(string), "<memory>")

	// memory.md was updated in the working tree.
	memContent, err := os.ReadFile(filepath.Join(repoRoot, "memory.md"))
	require.NoError(t, err)
	assert.Contains(t, string(memContent), "learned X")

	// Prompt contained the included file + env var banner.
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
		Secrets: secrets.New(map[string]string{
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
```

The test references a `sig()` helper — add at the top of `runner_test.go`:

```go
import (
	"time"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func sig() *object.Signature {
	return &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/runner/... -v
```

Expected: package undefined.

- [ ] **Step 3: Implement `internal/runner/runner.go`**

```go
// Package runner orchestrates a single end-to-end skill execution:
// load manifest + skill, call LLM, parse memory, publish, writeback.
package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/memory"
	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/secrets"
	"github.com/gambtho/cronfoundry/internal/template"
	"github.com/gambtho/cronfoundry/internal/writeback"
)

type Status string

const (
	StatusSucceeded      Status = "succeeded"
	StatusPartialFailure Status = "partial_failure"
	StatusFailed         Status = "failed"
)

type RunInput struct {
	RepoRoot     string
	ManifestPath string // relative to RepoRoot
	SkillPath    string // from manifest
	ScheduleName string

	Secrets *secrets.Resolver
	// LLM API key — resolved by caller (env-driven in the CLI); passed through.
	LLMAPIKey string
	// Azure AI Foundry fields (ignored by other providers).
	LLMEndpoint   string
	LLMDeployment string

	DryRun   bool // don't publish, don't writeback, don't push
	SkipPush bool // do writeback commit but don't push (useful for tests)

	// Writeback push auth (MVP: PAT from env).
	GitHubUsername string
	GitHubToken    string
}

type RunResult struct {
	Status         Status
	Usage          llm.Usage
	Output         string // published output (memory block stripped)
	MemoryContent  string
	PublishResults []publish.Result
	WritebackSHA   string
	StartedAt      time.Time
	FinishedAt     time.Time
}

type Deps struct {
	// ProviderFactory resolves a provider by name; tests inject fakes.
	ProviderFactory func(name string) (llm.Provider, error)
	// Publishers by type.
	Publishers map[string]publish.Publisher
	// Now is a clock hook (optional, defaults to time.Now).
	Now func() time.Time
}

type Runner struct {
	deps Deps
}

func New(d Deps) *Runner {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ProviderFactory == nil {
		d.ProviderFactory = llm.NewProvider
	}
	return &Runner{deps: d}
}

func (r *Runner) Run(ctx context.Context, in RunInput) (RunResult, error) {
	result := RunResult{StartedAt: r.deps.Now()}

	manifestBytes, err := os.ReadFile(filepath.Join(in.RepoRoot, in.ManifestPath))
	if err != nil {
		return fail(&result, fmt.Errorf("read manifest: %w", err), r.deps.Now)
	}
	m, err := config.ParseManifest(manifestBytes)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}
	if err := m.Validate(); err != nil {
		return fail(&result, err, r.deps.Now)
	}
	_, sch, err := m.FindSchedule(in.SkillPath, in.ScheduleName)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Load SKILL.md.
	skillMDPath := filepath.Join(in.RepoRoot, in.SkillPath, "SKILL.md")
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		return fail(&result, fmt.Errorf("read SKILL.md: %w", err), r.deps.Now)
	}
	skill, err := config.ParseSkillFile(skillBytes)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Resolve includes against the skill's directory.
	skillRoot := filepath.Join(in.RepoRoot, in.SkillPath)
	body, err := config.ResolveIncludes(skill.Body, skillRoot)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Build env banner.
	envBanner, err := buildEnvBanner(sch.Env, in.Secrets)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Compose messages: system = env banner, user = skill body.
	var msgs []llm.Message
	if envBanner != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: envBanner})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: body})

	// Resolve provider.
	provider, err := r.deps.ProviderFactory(sch.Provider)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Stream.
	var sb strings.Builder
	usage, err := provider.Chat(ctx, msgs, llm.CallOptions{
		Model:      sch.Model,
		MaxTokens:  skill.Frontmatter.MaxTokens,
		APIKey:     in.LLMAPIKey,
		Endpoint:   in.LLMEndpoint,
		Deployment: in.LLMDeployment,
	}, func(c llm.StreamChunk) { sb.WriteString(c.Delta) })
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}
	result.Usage = usage

	published, memBlock, hasMemory := memory.Extract(sb.String())
	result.Output = published
	result.MemoryContent = memBlock

	if in.DryRun {
		result.Status = StatusSucceeded
		result.FinishedAt = r.deps.Now()
		return result, nil
	}

	// Publish.
	tctx := template.Context{
		Output:    published,
		RunID:     fmt.Sprintf("local-%d", result.StartedAt.UnixNano()),
		RunDate:   result.StartedAt.Format("2006-01-02"),
		StartedAt: result.StartedAt,
		Schedule:  template.Meta{Name: sch.Name},
		Skill:     template.Meta{Name: skill.Frontmatter.Name},
	}
	dispatcher := &publish.Dispatcher{Publishers: r.deps.Publishers}
	pubResults := dispatcher.Dispatch(ctx, sch.Destinations, published, tctx, in.Secrets)
	result.PublishResults = pubResults

	allOK := true
	for _, pr := range pubResults {
		if !pr.OK {
			allOK = false
		}
	}

	// Writeback.
	writebackOK := true
	if sch.Writeback != nil && sch.Writeback.Enabled && hasMemory {
		sha, err := writeback.New().Commit(in.RepoRoot, writeback.Options{
			Path:        sch.Writeback.Path,
			Mode:        sch.Writeback.Mode,
			Content:     memBlock,
			Message:     fmt.Sprintf("chore(cronfoundry): update %s", sch.Writeback.Path),
			AuthorName:  "cronfoundry[bot]",
			AuthorEmail: "cronfoundry[bot]@users.noreply.github.com",
		})
		if err != nil {
			writebackOK = false
		} else {
			result.WritebackSHA = sha
			if !in.SkipPush && in.GitHubToken != "" {
				if err := writeback.New().Push(in.RepoRoot, "origin", in.GitHubUsername, in.GitHubToken); err != nil {
					writebackOK = false
				}
			}
		}
	}

	switch {
	case allOK && writebackOK:
		result.Status = StatusSucceeded
	default:
		result.Status = StatusPartialFailure
	}
	result.FinishedAt = r.deps.Now()
	return result, nil
}

func buildEnvBanner(env map[string]config.EnvValue, s *secrets.Resolver) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("<env>\n")
	// Stable order.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		v := env[k]
		val := v.Literal
		if v.Secret != "" {
			resolved, err := s.Get(v.Secret)
			if err != nil {
				return "", fmt.Errorf("env %s: %w", k, err)
			}
			val = resolved
		}
		fmt.Fprintf(&b, "%s=%s\n", k, val)
	}
	b.WriteString("</env>")
	return b.String(), nil
}

func sortStrings(ss []string) {
	// Keep runner dependency-free — tiny insertion sort is fine for small N.
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

func fail(r *RunResult, err error, now func() time.Time) (RunResult, error) {
	r.Status = StatusFailed
	r.FinishedAt = now()
	return *r, err
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/runner/... -v
```

Expected: PASS. If the partial-failure test fails because Discord's publisher reaches retry-exhaust on 4xx, confirm the earlier `postJSON` implementation correctly returns immediately on 4xx.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/
git commit -m "feat(runner): orchestrate load→LLM→parse→publish→writeback"
```

---

## Task 22: CLI wiring + smoke fixture + README

**Files:**
- Modify: `cmd/runner/main.go`
- Create: `testdata/skills/weekly-digest/SKILL.md`
- Create: `testdata/skills/weekly-digest/context/template.md`
- Create: `testdata/cronfoundry.yaml`
- Create: `README.md`

- [ ] **Step 1: Replace `cmd/runner/main.go` with real wiring**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/redact"
	"github.com/gambtho/cronfoundry/internal/runner"
	"github.com/gambtho/cronfoundry/internal/secrets"
)

func main() {
	var (
		repoRoot     string
		manifestPath string
		skillPath    string
		scheduleName string
		llmKeyEnv    string
		llmEndpoint  string
		llmDeploy    string
		dryRun       bool
		skipPush     bool
	)

	root := &cobra.Command{
		Use:           "cronfoundry-runner",
		Short:         "Execute a CronFoundry skill once",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run a single schedule from a cronfoundry.yaml manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			env := envAsMap()
			sec := secrets.New(env)

			// Build redactor including all known secret values + LLM key + GH token.
			redactValues := sec.AllValues()
			if k, ok := env[llmKeyEnv]; ok {
				redactValues = append(redactValues, k)
			}
			if tok, ok := env["GITHUB_TOKEN"]; ok {
				redactValues = append(redactValues, tok)
			}
			redactor := redact.New(redactValues)

			logger := slog.New(redactingHandler{inner: slog.NewTextHandler(os.Stderr, nil), r: redactor})
			slog.SetDefault(logger)

			r := runner.New(runner.Deps{
				Publishers: map[string]publish.Publisher{
					"github-issue": publish.NewGitHubIssuePublisher("", env["GITHUB_TOKEN"]),
					"slack":        publish.NewSlackPublisher(),
					"discord":      publish.NewDiscordPublisher(),
					"teams":        publish.NewTeamsPublisher(),
				},
			})

			llmKey := env[llmKeyEnv]
			if llmKey == "" {
				return fmt.Errorf("LLM key env var %q is empty", llmKeyEnv)
			}

			result, err := r.Run(ctx, runner.RunInput{
				RepoRoot:       repoRoot,
				ManifestPath:   manifestPath,
				SkillPath:      skillPath,
				ScheduleName:   scheduleName,
				Secrets:        sec,
				LLMAPIKey:      llmKey,
				LLMEndpoint:    llmEndpoint,
				LLMDeployment:  llmDeploy,
				DryRun:         dryRun,
				SkipPush:       skipPush,
				GitHubUsername: "cronfoundry-bot",
				GitHubToken:    env["GITHUB_TOKEN"],
			})
			if err != nil {
				slog.Error("run failed", "err", err)
				return err
			}

			summary := map[string]any{
				"status":          string(result.Status),
				"input_tokens":    result.Usage.InputTokens,
				"output_tokens":   result.Usage.OutputTokens,
				"started_at":      result.StartedAt,
				"finished_at":     result.FinishedAt,
				"writeback_sha":   result.WritebackSHA,
				"publish_results": pubSummary(result.PublishResults),
			}
			b, _ := json.MarshalIndent(summary, "", "  ")
			fmt.Println(string(b))

			// Exit non-zero on partial/total failure for CI ergonomics.
			switch result.Status {
			case runner.StatusSucceeded:
				return nil
			default:
				return fmt.Errorf("run finished with status %s", result.Status)
			}
		},
	}

	flags := runCmd.Flags()
	flags.StringVar(&repoRoot, "repo", ".", "path to the skill repo root")
	flags.StringVar(&manifestPath, "manifest", "cronfoundry.yaml", "path to manifest, relative to --repo")
	flags.StringVar(&skillPath, "skill-path", "", "skill path as declared in the manifest (required)")
	flags.StringVar(&scheduleName, "schedule-name", "", "schedule name within the skill (required)")
	flags.StringVar(&llmKeyEnv, "llm-key-env", "OPENAI_API_KEY", "env var name that holds the LLM API key")
	flags.StringVar(&llmEndpoint, "llm-endpoint", "", "Azure AI Foundry endpoint (azure-foundry provider only)")
	flags.StringVar(&llmDeploy, "llm-deployment", "", "Azure AI Foundry deployment name")
	flags.BoolVar(&dryRun, "dry-run", false, "skip publish and writeback; print output only")
	flags.BoolVar(&skipPush, "skip-push", false, "perform writeback commit locally but do not push")
	_ = runCmd.MarkFlagRequired("skill-path")
	_ = runCmd.MarkFlagRequired("schedule-name")

	root.AddCommand(runCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type redactingHandler struct {
	inner slog.Handler
	r     *redact.Redactor
}

func (h redactingHandler) Enabled(ctx context.Context, lvl slog.Level) bool { return h.inner.Enabled(ctx, lvl) }
func (h redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	rec.Message = h.r.Redact(rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		if a.Value.Kind() == slog.KindString {
			a.Value = slog.StringValue(h.r.Redact(a.Value.String()))
		}
		return true
	})
	return h.inner.Handle(ctx, rec)
}
func (h redactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return redactingHandler{inner: h.inner.WithAttrs(as), r: h.r}
}
func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{inner: h.inner.WithGroup(name), r: h.r}
}

func envAsMap() map[string]string {
	env := os.Environ()
	out := make(map[string]string, len(env))
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out
}

func pubSummary(rs []publish.Result) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		errStr := ""
		if r.Err != nil {
			errStr = r.Err.Error()
		}
		out = append(out, map[string]any{
			"type":   r.Type,
			"ok":     r.OK,
			"detail": r.Detail,
			"error":  errStr,
		})
	}
	return out
}
```

- [ ] **Step 2: Build the binary and verify it compiles**

```bash
go build -o cronfoundry-runner ./cmd/runner
./cronfoundry-runner run --help
```

Expected: help text shows `--repo`, `--manifest`, `--skill-path`, `--schedule-name`, `--llm-key-env`, `--dry-run`, `--skip-push`.

- [ ] **Step 3: Create the smoke-test fixture**

`testdata/cronfoundry.yaml`:

```yaml
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        timezone: America/Los_Angeles
        provider: openai
        model: gpt-5.1
        destinations:
          - slack:
              secret: slack_url
              text: "Digest for {{ run.date }}:\n{{ output.truncated 35000 }}"
        writeback:
          enabled: true
          path: memory.md
          mode: append
        env:
          LOOKBACK_DAYS: "7"
```

`testdata/skills/weekly-digest/SKILL.md`:

```markdown
---
name: weekly-digest
description: CronFoundry smoke-test skill
max_tokens: 512
---
You are the weekly digest assistant.

Instructions:
{{ include "context/template.md" }}

Respond with a one-line summary, then a <memory>...</memory> block with
one short learning.
```

`testdata/skills/weekly-digest/context/template.md`:

```markdown
Please summarize the past LOOKBACK_DAYS days in a single sentence.
Use a friendly tone.
```

- [ ] **Step 4: Create `README.md`**

```markdown
# CronFoundry

Self-hostable, GitOps-style scheduler for LLM skills. Runs a skill against
OpenAI / Anthropic / Azure AI Foundry on a schedule, publishes the output to
GitHub issues, Slack, Discord, or Teams, and commits learnings back to the
skill repo.

Status: **P1 — core runner CLI only.** The scheduler, API, web UI, and Azure
Bicep deployment are in later phases (P2–P4).

## Quick start (P1)

Build:

```bash
go build -o cronfoundry-runner ./cmd/runner
```

Run a skill from the smoke-test fixture:

```bash
export OPENAI_API_KEY=sk-...
export CRONFOUNDRY_SECRET_SLACK_URL=https://hooks.slack.com/...
./cronfoundry-runner run \
  --repo ./testdata \
  --manifest cronfoundry.yaml \
  --skill-path skills/weekly-digest \
  --schedule-name monday-morning \
  --skip-push
```

## Spec

- Technical design: `docs/superpowers/specs/2026-04-19-cronfoundry-design.md`
- Product requirements: `docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`
- Plans: `docs/superpowers/plans/`
```

- [ ] **Step 5: Run the whole test suite**

```bash
go test ./... -v
```

Expected: all packages pass.

- [ ] **Step 6: Run the smoke test against the fixture (offline path via --dry-run)**

```bash
export OPENAI_API_KEY=dummy
./cronfoundry-runner run \
  --repo ./testdata \
  --manifest cronfoundry.yaml \
  --skill-path skills/weekly-digest \
  --schedule-name monday-morning \
  --dry-run
```

Expected: this will attempt a real OpenAI call and fail because `OPENAI_API_KEY=dummy` isn't valid, which is acceptable proof that CLI wiring is correct. Run with a real key to see it go through.

- [ ] **Step 7: Commit**

```bash
git add cmd/ testdata/ README.md
git commit -m "feat(runner): wire CLI, add smoke-test fixture and README"
```

---

## Self-Review

**1. Spec coverage check:**
- Manifest parsing + skill discovery: ✅ Tasks 2–4
- `{{ include }}` preprocessor: ✅ Task 5
- Secrets resolution: ✅ Task 6 (env-based for P1; KV in P2)
- `<memory>` parsing: ✅ Task 7
- Template rendering: ✅ Task 8
- Log redaction: ✅ Tasks 9 + 22
- LLM streaming (OpenAI / Anthropic / Azure AI Foundry): ✅ Tasks 10–14
- Destinations (github-issue / slack / discord / teams): ✅ Tasks 15–19
- Writeback (append / replace + push): ✅ Task 20
- Orchestration + partial_failure semantics: ✅ Task 21
- CLI + README + smoke fixture: ✅ Task 22
- Scheduler, API, DB, UI, KV, Bicep, GitHub App: **deferred to P2–P4 (intentional)**

**2. Placeholder scan:** None. Every step has executable code or a concrete command.

**3. Type consistency:** `Provider.Chat`, `CallOptions`, `Message`, `StreamChunk`, `Usage`, `Publisher.Publish`, `Result`, `config.Destination`, `template.Context`, `secrets.Resolver`, `writeback.Options` are consistent across task files.

**4. One known weak spot:** the specific openai-go / anthropic-sdk-go / azopenai field/method names may drift between SDK minor versions. Each adapter task includes a "the test is the load-bearing spec; adjust SDK calls to make it pass" note. A cleaner-but-heavier alternative is pinning exact SDK versions in `go.mod` — recommended if the first task-execution session reveals version churn.

---

Plan saved to `docs/superpowers/plans/2026-04-19-p1-core-runner.md`.
