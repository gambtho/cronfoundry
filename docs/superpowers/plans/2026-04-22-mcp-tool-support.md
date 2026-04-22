# MCP Tool Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the MCP tool-support feature specified in `docs/superpowers/specs/2026-04-22-mcp-tool-support-design.md`: skills declare stdio MCP servers in their `SKILL.md` frontmatter; the runner launches them per fire, runs a multi-turn tool-use loop against the LLM, then continues the existing publish → writeback pipeline.

**Architecture:** Ships as a single cohesive feature across five phases:

1. **DB + config + sync** — persistent plumbing for the new YAML fields.
2. **`internal/mcp` package** — stdio JSON-RPC client, process manager, dispatch.
3. **LLM provider extension** — `ToolCapableProvider` interface with `ChatTurn`, implemented for Anthropic and OpenAI.
4. **Runner integration** — three-phase execution (setup → multi-turn loop → teardown) in `internal/runner`, guarded by provider capability check.
5. **UI + image + final gate** — Dashboard surface, runner Dockerfile with Node/Python/uvx, full test suite.

Each phase is independently testable; phases 1–3 can be merged as smaller PRs if preferred, with phase 4 tying them together.

**Tech Stack:** Go 1.22+, pgx, sqlc, anthropic-sdk-go, openai-go, stretchr/testify, React + TypeScript. No new external Go module — MCP's JSON-RPC protocol is implemented directly.

**Pre-flight:**
- Work happens on branch `spec/mcp-tools` in worktree `.worktrees/spec-mcp-tools`.
- Run `go test ./... -timeout 10m` (or `make test`, which needs docker) before each commit.
- Run `go vet ./...` before each commit.
- Run `make sqlc` after any `internal/db/queries/` or `internal/db/migrations/` edit.
- Run `cd web && npm run build` after web edits.

---

## File structure (touched)

### Phase 1 — DB + config + sync

| Path | Responsibility | Action |
| --- | --- | --- |
| `internal/db/migrations/20260422000002_mcp.sql` | Schema migration for `mcp_env_json`, `max_turns` on `schedule` | Create |
| `internal/db/schema.sql` | sqlc introspection cache | Modify |
| `internal/db/queries/schedule.sql` | `UpsertSchedule` | Modify (thread `mcp_env_json`, `max_turns`) |
| `internal/db/gen/**` | Generated | Regenerate |
| `internal/config/skill.go` | `SkillFrontmatter` | Modify (`MCPServers`, `MaxTurns`) |
| `internal/config/skill_test.go` | Skill parsing tests | Modify |
| `internal/config/manifest.go` | `Schedule` | Modify (`MCPEnv`, `MaxTurns`) |
| `internal/config/manifest_test.go` | Manifest parsing tests | Modify |
| `internal/sync/upsert.go` | YAML → DB | Modify (cross-validation, persist new fields) |
| `internal/sync/upsert_test.go` | Sync tests | Modify |

### Phase 2 — `internal/mcp` package

| Path | Responsibility | Action |
| --- | --- | --- |
| `internal/mcp/protocol.go` | JSON-RPC types, MCP request/response shapes | Create |
| `internal/mcp/client.go` | Single-server stdio client: initialize, list_tools, call_tool | Create |
| `internal/mcp/client_test.go` | Client unit tests vs. stub server | Create |
| `internal/mcp/process.go` | Subprocess spawn + graceful shutdown | Create |
| `internal/mcp/manager.go` | Multi-server manager, parallel dispatch | Create |
| `internal/mcp/manager_test.go` | Manager unit tests | Create |
| `testdata/mcp-fixtures/stub-server/main.go` | Go stub MCP server used by all tests | Create |

### Phase 3 — LLM provider extension

| Path | Responsibility | Action |
| --- | --- | --- |
| `internal/llm/provider.go` | Shared types | Modify (`RoleAssistant`, `RoleTool`, `ToolDef`, `ToolUse`, `TurnResult`, `ToolCapableProvider`) |
| `internal/llm/anthropic.go` | Anthropic adapter | Modify (implement `ChatTurn`) |
| `internal/llm/anthropic_test.go` | Anthropic tests | Modify |
| `internal/llm/openai.go` | OpenAI adapter | Modify (implement `ChatTurn`) |
| `internal/llm/openai_test.go` | OpenAI tests | Modify |

### Phase 4 — Runner integration

| Path | Responsibility | Action |
| --- | --- | --- |
| `internal/runner/runner.go` | Run orchestration | Modify (three-phase tool-aware path) |
| `internal/runner/runner_test.go` | Runner unit tests | Modify |
| `internal/api/run_context.go` | `/internal/runs/:id/context` | Modify (include `mcp_servers`, `mcp_env`, `max_turns`) |
| `internal/api/run_context_test.go` | Context handler tests | Modify |
| `internal/db/queries/run.sql` | `GetRunForContext` | Modify (select new columns) |
| `cmd/cronfoundry/runner.go` | Production HTTP-mode runner wiring | Modify (receive new context fields, pass to runner) |
| `cmd/cronfoundry/e2e_test.go` | End-to-end | Modify (add MCP case using stub server) |

### Phase 5 — UI + image + final gate

| Path | Responsibility | Action |
| --- | --- | --- |
| `web/src/lib/types.ts` | `Schedule`, `RunDetail` | Modify (add tool-related fields) |
| `web/src/pages/Dashboard.tsx` | Schedule cards | Modify (tool icon) |
| `web/src/pages/Runs.tsx` | Run detail + timeline | Modify (turn count, tool-call event rows) |
| `deploy/Dockerfile.runner` | **NEW** — runner image with Node/Python/uvx | Create |
| `deploy/Dockerfile` | Existing image | Modify (retain for API+scheduler; remove runner binary) |

---

# Phase 1 — DB + config + sync

## Task 1: Migration for `mcp_env_json` and `max_turns` on schedule

**Files:**
- Create: `internal/db/migrations/20260422000002_mcp.sql`
- Modify: `internal/db/schema.sql`

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
ALTER TABLE schedule
  ADD COLUMN mcp_env_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN max_turns    int;

-- +goose Down
ALTER TABLE schedule
  DROP COLUMN mcp_env_json,
  DROP COLUMN max_turns;
```

- [ ] **Step 2: Update `internal/db/schema.sql`**

Find the schedule `CREATE TABLE` block and append two lines:

```sql
    env_json            jsonb NOT NULL DEFAULT '{}'::jsonb,
    mcp_env_json        jsonb NOT NULL DEFAULT '{}'::jsonb,
    max_turns           int,
    next_fire_at        timestamptz,
```

- [ ] **Step 3: Verify migration applies**

```bash
go test ./internal/db/... -run Migrate -timeout 120s
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/20260422000002_mcp.sql internal/db/schema.sql
git commit -m "feat(db): add mcp_env_json and max_turns columns to schedule"
```

---

## Task 2: Regenerate sqlc for new schedule columns

- [ ] **Step 1: Run sqlc**

```bash
make sqlc
```

- [ ] **Step 2: Inspect `internal/db/gen/models.go`**

Expected: `Schedule` struct now has `McpEnvJson []byte` and `MaxTurns *int32`.

- [ ] **Step 3: `go build ./...`**

Expected: PASS (no callers reference the new fields yet).

- [ ] **Step 4: Commit**

```bash
git add internal/db/gen/
git commit -m "chore(db): regenerate sqlc for mcp columns"
```

---

## Task 3: Parse `mcp_servers` + `max_turns` from `SKILL.md` frontmatter

**Files:**
- Modify: `internal/config/skill.go`
- Modify: `internal/config/skill_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/skill_test.go`:

```go
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
```

Note: this introduces `Skill.Validate()` — we'll add it below. Existing tests that don't call `Validate()` remain intact.

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/config/ -run TestParseSkillFile_MCPServers -v
```

Expected: compile error.

- [ ] **Step 3: Extend `internal/config/skill.go`**

Add to the types block:

```go
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}
```

Extend `SkillFrontmatter`:

```go
type SkillFrontmatter struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	ModelHint   string                    `json:"model_hint"`
	MaxTokens   int                       `json:"max_tokens"`
	MaxTurns    int                       `json:"max_turns"`
	Writeback   SkillWritebackFrontmatter `json:"writeback"`
	MCPServers  []MCPServer               `json:"mcp_servers,omitempty"`
}
```

Add a `Validate` method:

```go
import "regexp"

var mcpServerNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Validate returns the first schema violation found, or nil.
func (s *Skill) Validate() error {
	seen := map[string]bool{}
	for i, ms := range s.Frontmatter.MCPServers {
		if !mcpServerNameRe.MatchString(ms.Name) {
			return fmt.Errorf("skill %q: mcp_servers[%d].name %q invalid (want: %s)",
				s.Frontmatter.Name, i, ms.Name, mcpServerNameRe.String())
		}
		if seen[ms.Name] {
			return fmt.Errorf("skill %q: duplicate mcp_servers name %q", s.Frontmatter.Name, ms.Name)
		}
		seen[ms.Name] = true
		if ms.Command == "" {
			return fmt.Errorf("skill %q: mcp_servers[%d] %q: command required",
				s.Frontmatter.Name, i, ms.Name)
		}
	}
	if s.Frontmatter.MaxTurns < 0 {
		return fmt.Errorf("skill %q: max_turns must be >= 0", s.Frontmatter.Name)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/ -run TestParseSkillFile -v
```

Expected: all 4 new test funcs PASS. Existing skill tests unaffected.

- [ ] **Step 5: Commit**

```bash
git add internal/config/skill.go internal/config/skill_test.go
git commit -m "feat(config): parse mcp_servers and max_turns in SKILL.md frontmatter"
```

---

## Task 4: Parse `mcp_env` + `max_turns` at the schedule level in `cronfoundry.yaml`

**Files:**
- Modify: `internal/config/manifest.go`
- Modify: `internal/config/manifest_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/manifest_test.go`:

```go
func TestParseManifest_MCPEnvAndMaxTurns(t *testing.T) {
	src := []byte(`version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday
        cron: "0 9 * * MON"
        provider: anthropic
        model: claude-opus-4-7
        max_turns: 40
        env:
          LOOKBACK_DAYS: "7"
        mcp_env:
          github:
            GITHUB_PERSONAL_ACCESS_TOKEN:
              secret: github_mcp_pat
          fetch: {}
`)
	m, err := ParseManifest(src)
	require.NoError(t, err)
	sch := m.Skills[0].Schedules[0]
	assert.Equal(t, 40, sch.MaxTurns)
	require.Contains(t, sch.MCPEnv, "github")
	require.Contains(t, sch.MCPEnv, "fetch")
	tok, ok := sch.MCPEnv["github"]["GITHUB_PERSONAL_ACCESS_TOKEN"]
	require.True(t, ok)
	assert.Equal(t, "github_mcp_pat", tok.Secret)
	// Empty server env map is valid.
	assert.Empty(t, sch.MCPEnv["fetch"])
}

func TestParseManifest_RejectsNegativeMaxTurns(t *testing.T) {
	src := []byte(`version: 1
skills:
  - path: skills/a
    schedules:
      - name: s
        cron: "* * * * *"
        provider: anthropic
        model: x
        max_turns: -1
`)
	m, err := ParseManifest(src)
	require.NoError(t, err)
	require.Error(t, m.Validate())
	assert.Contains(t, m.Validate().Error(), "max_turns must be >= 1")
}
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/config/ -run TestParseManifest_MCP -v
```

- [ ] **Step 3: Extend `internal/config/manifest.go`**

Add fields to `Schedule`:

```go
type Schedule struct {
	Name          string                                  `json:"name"`
	Cron          string                                  `json:"cron"`
	Timezone      string                                  `json:"timezone"`
	OverlapPolicy string                                  `json:"overlap_policy"`
	TimeoutSec    int                                     `json:"timeout_sec"`
	Provider      string                                  `json:"provider"`
	Model         string                                  `json:"model"`
	MaxTurns      int                                     `json:"max_turns,omitempty"`
	Destinations  []Destination                           `json:"destinations"`
	Writeback     *WritebackConfig                        `json:"writeback,omitempty"`
	Env           map[string]EnvValue                     `json:"env"`
	MCPEnv        map[string]map[string]EnvValue          `json:"mcp_env,omitempty"`
}
```

Extend `Manifest.Validate()` with one new rule inside the per-schedule loop (adjacent to the existing Cron/Provider/Model checks):

```go
if sch.MaxTurns < 0 {
	return fmt.Errorf("skill %q schedule %q: max_turns must be >= 1 (got %d)", s.Path, sch.Name, sch.MaxTurns)
}
```

(Note: `0` is valid and means "unset", falling back to skill frontmatter → global default. Negative is the rejection case. Adjust the error message accordingly — kept the message ">= 1" since 0 is the unset sentinel not a meaningful setting.)

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/ -run TestParseManifest_MCP -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/manifest.go internal/config/manifest_test.go
git commit -m "feat(config): parse mcp_env and max_turns at schedule level"
```

---

## Task 5: Cross-validate skill-declared servers against schedule `mcp_env`

**Files:**
- Modify: `internal/sync/upsert.go`
- Modify: `internal/sync/upsert_test.go`

This is where we enforce the three cross-file rules:

1. Every `mcp_servers[].name` in `SKILL.md` must appear in the schedule's `mcp_env` (even as `{}`).
2. Every key in the schedule's `mcp_env` must reference a declared server.
3. `provider: azure-foundry` + non-empty `mcp_servers` → reject (MVP scope).

- [ ] **Step 1: Write the failing test**

Append to `internal/sync/upsert_test.go`:

```go
func TestUpsert_RejectsMissingMCPEnv(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	orgID, repoID := seedOrgAndRepo(t, pool) // existing helper

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name: "weekly-digest",
			MCPServers: []config.MCPServer{
				{Name: "github", Command: "npx"},
			},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "anthropic", Model: "claude-opus-4-7",
				// mcp_env is MISSING for declared 'github' server.
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp_env missing")
}

func TestUpsert_RejectsStrayMCPEnv(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	orgID, repoID := seedOrgAndRepo(t, pool)

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{Name: "weekly-digest"},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "anthropic", Model: "claude-opus-4-7",
				MCPEnv: map[string]map[string]config.EnvValue{
					"github": {}, // server not declared on skill
				},
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp_env references undeclared server")
}

func TestUpsert_RejectsAzureFoundryWithMCPServers(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	orgID, repoID := seedOrgAndRepo(t, pool)

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name: "weekly-digest",
			MCPServers: []config.MCPServer{{Name: "github", Command: "npx"}},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "azure-foundry", Model: "gpt-4o",
				MCPEnv: map[string]map[string]config.EnvValue{"github": {}},
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azure-foundry does not support mcp_servers")
}

func TestUpsert_PersistsMCPEnvAndMaxTurns(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	orgID, repoID := seedOrgAndRepo(t, pool)

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name:       "weekly-digest",
			MCPServers: []config.MCPServer{{Name: "github", Command: "npx"}},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "anthropic", Model: "claude-opus-4-7",
				MaxTurns: 40,
				MCPEnv: map[string]map[string]config.EnvValue{
					"github": {"GITHUB_PAT": {Secret: "github_mcp_pat"}},
				},
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.NoError(t, err)

	var mcpEnvJSON []byte
	var maxTurns *int32
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT mcp_env_json, max_turns FROM schedule WHERE name='monday'`).Scan(&mcpEnvJSON, &maxTurns))
	assert.Contains(t, string(mcpEnvJSON), "github")
	assert.Contains(t, string(mcpEnvJSON), "GITHUB_PAT")
	require.NotNil(t, maxTurns)
	assert.EqualValues(t, 40, *maxTurns)
}
```

Add `seedOrgAndRepo` to `upsert_test.go` if not already present; model on existing sync-test helpers (search the file).

- [ ] **Step 2: Confirm failures**

```bash
go test ./internal/sync/ -run "TestUpsert_(Rejects|PersistsMCP)" -v
```

Expected: tests FAIL (cross-validation not enforced; new fields not persisted).

- [ ] **Step 3: Extend `internal/sync/upsert.go`**

First: extend the `UpsertSchedule` query so the new columns can be written.

In `internal/db/queries/schedule.sql`, replace `UpsertSchedule` to include `mcp_env_json` and `max_turns` — add them to the VALUES list and the DO UPDATE block, following the same pattern as `env_json`:

```sql
INSERT INTO schedule (
    org_id, skill_id, name, cron, timezone, overlap_policy, timeout_sec,
    enabled, provider, model, llm_secret_ref, llm_endpoint, llm_deployment,
    destinations_json, writeback_json, env_json, mcp_env_json, max_turns, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, now())
ON CONFLICT (skill_id, name) DO UPDATE
  SET ...,
      env_json          = EXCLUDED.env_json,
      mcp_env_json      = EXCLUDED.mcp_env_json,
      max_turns         = EXCLUDED.max_turns,
      updated_at        = now()
RETURNING *;
```

Run `make sqlc`.

Then in `internal/sync/upsert.go`, before the `q.UpsertSchedule` call, add cross-validation:

```go
// Cross-file validation: mcp_servers (from skill) ↔ mcp_env (from schedule).
if len(sk.Frontmatter.MCPServers) > 0 {
	if sch.Provider == "azure-foundry" {
		return fmt.Errorf("sync: skill %q schedule %q: azure-foundry does not support mcp_servers in this release", entry.Path, sch.Name)
	}
	declared := map[string]bool{}
	for _, ms := range sk.Frontmatter.MCPServers {
		declared[ms.Name] = true
		if _, ok := sch.MCPEnv[ms.Name]; !ok {
			return fmt.Errorf("sync: skill %q schedule %q: mcp_env missing for declared server %q (use {} if no env needed)", entry.Path, sch.Name, ms.Name)
		}
	}
	for k := range sch.MCPEnv {
		if !declared[k] {
			return fmt.Errorf("sync: skill %q schedule %q: mcp_env references undeclared server %q", entry.Path, sch.Name, k)
		}
	}
}

mcpEnvBytes := []byte(`{}`)
if len(sch.MCPEnv) > 0 {
	mcpEnvBytes, err = json.Marshal(sch.MCPEnv)
	if err != nil {
		return fmt.Errorf("sync: marshal mcp_env for %q/%q: %w", entry.Path, sch.Name, err)
	}
}

var maxTurns *int32
if sch.MaxTurns > 0 {
	v := int32(sch.MaxTurns)
	maxTurns = &v
}
```

Add `McpEnvJson: mcpEnvBytes, MaxTurns: maxTurns` to the `UpsertScheduleParams{...}` literal.

- [ ] **Step 4: Run tests**

```bash
make sqlc
go test ./internal/sync/ -run "TestUpsert_(Rejects|PersistsMCP)" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/queries/schedule.sql internal/db/gen/ internal/sync/upsert.go internal/sync/upsert_test.go
git commit -m "feat(sync): persist mcp_env/max_turns and enforce cross-file MCP rules"
```

---

# Phase 2 — `internal/mcp` package

## Task 6: Write the Go stub MCP server used by every test

**Files:**
- Create: `testdata/mcp-fixtures/stub-server/main.go`
- Create: `testdata/mcp-fixtures/stub-server/go.mod` (optional — only if the module is separate)

Decision: keep it as part of the main Go module. The test fixture is built on demand by each test file that needs it (via `go build` in a `TestMain`).

- [ ] **Step 1: Write the stub**

Create `testdata/mcp-fixtures/stub-server/main.go`:

```go
// Stub MCP server for tests.
//
// Behavior is controlled by env vars:
//
//   MCP_STUB_EXIT_ON_INIT=1      exit 1 during initialize
//   MCP_STUB_CRASH_ON_CALL=1     exit 1 mid-tools/call
//   MCP_STUB_SLEEP_MS=5000       sleep N ms inside tools/call before replying
//   MCP_STUB_RETURN_ERROR=1      respond to tools/call with a JSON-RPC error result
//   MCP_STUB_TOOL_NAME=echo      tool name in tools/list (default "echo")
//
// The stub speaks MCP 2024-11-05 over stdio. Each incoming JSON-RPC
// request produces exactly one response (no notifications).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

type req struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type resp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	if os.Getenv("MCP_STUB_EXIT_ON_INIT") == "1" {
		// Exit without sending anything, once the first line arrives.
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		os.Exit(1)
	}

	toolName := os.Getenv("MCP_STUB_TOOL_NAME")
	if toolName == "" {
		toolName = "echo"
	}

	enc := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<16), 1<<20)

	for sc.Scan() {
		var r req
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		switch r.Method {
		case "initialize":
			raw, _ := json.Marshal(map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "stub", "version": "0.1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})
			writeResp(enc, r.ID, raw, nil)
		case "tools/list":
			raw, _ := json.Marshal(map[string]any{
				"tools": []map[string]any{{
					"name":        toolName,
					"description": "stub tool",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
			writeResp(enc, r.ID, raw, nil)
		case "tools/call":
			if os.Getenv("MCP_STUB_CRASH_ON_CALL") == "1" {
				os.Exit(1)
			}
			if ms, err := strconv.Atoi(os.Getenv("MCP_STUB_SLEEP_MS")); err == nil && ms > 0 {
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
			if os.Getenv("MCP_STUB_RETURN_ERROR") == "1" {
				writeResp(enc, r.ID, nil, &rpcErr{Code: -32000, Message: "stub error"})
				continue
			}
			raw, _ := json.Marshal(map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "ok"},
				},
			})
			writeResp(enc, r.ID, raw, nil)
		default:
			writeResp(enc, r.ID, nil, &rpcErr{Code: -32601, Message: fmt.Sprintf("method not found: %s", r.Method)})
		}
	}
}

func writeResp(enc *json.Encoder, id json.RawMessage, result json.RawMessage, e *rpcErr) {
	_ = enc.Encode(resp{JSONRPC: "2.0", ID: id, Result: result, Error: e})
}
```

- [ ] **Step 2: Smoke-build it locally**

```bash
go build -o /tmp/mcp-stub ./testdata/mcp-fixtures/stub-server
echo '{"jsonrpc":"2.0","id":1,"method":"initialize"}' | /tmp/mcp-stub | head -c 200
```

Expected: one JSON line with `"protocolVersion":"2024-11-05"`.

- [ ] **Step 3: Commit**

```bash
git add testdata/mcp-fixtures/
git commit -m "test: add stub MCP server fixture for mcp package tests"
```

---

## Task 7: Implement `internal/mcp/protocol.go`

**Files:**
- Create: `internal/mcp/protocol.go`

No tests at this task — these are pure types consumed by the client in Task 8.

- [ ] **Step 1: Write the file**

```go
// Package mcp implements a minimal MCP 2024-11-05 stdio client: just
// enough of the protocol to initialize a server, enumerate its tools,
// call tools, and cancel in-flight calls. Resources, prompts, sampling,
// and roots are intentionally out of scope (see deferred in the spec).
package mcp

import "encoding/json"

// jsonrpcRequest is a single JSON-RPC 2.0 request over stdio.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a single JSON-RPC 2.0 response. Exactly one of
// Result or Error is set.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// initializeParams is MCP's initialize request payload. We announce minimal
// capabilities — we don't serve roots, sampling, resources, or prompts.
type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]any         `json:"capabilities"`
	ClientInfo      map[string]string      `json:"clientInfo"`
}

// listToolsResult is the 'result' payload of tools/list.
type listToolsResult struct {
	Tools []toolWire `json:"tools"`
}

type toolWire struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// callToolParams is the 'params' payload of tools/call.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Tool is the public representation of an MCP-advertised tool, exposed
// to Manager consumers.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// protocolVersion is the MCP spec version we speak.
const protocolVersion = "2024-11-05"
```

- [ ] **Step 2: `go build ./...`**

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/protocol.go
git commit -m "feat(mcp): protocol types for JSON-RPC + MCP wire shapes"
```

---

## Task 8: Implement `internal/mcp/client.go` + tests

**Files:**
- Create: `internal/mcp/client.go`
- Create: `internal/mcp/client_test.go`

`client` speaks to a single MCP server over a pair of `io.ReadCloser` / `io.WriteCloser` (typically `cmd.Stdout` / `cmd.Stdin`). Process lifecycle is NOT client's responsibility — that's Manager/process.go.

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/client_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubBin is lazily built once per test binary run.
var (
	stubBinOnce sync.Once
	stubBinPath string
	stubBinErr  error
)

func stub(t *testing.T) string {
	t.Helper()
	stubBinOnce.Do(func() {
		if testing.Short() {
			stubBinErr = nil
			return
		}
		dir := t.TempDir()
		out := filepath.Join(dir, "mcp-stub")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		repoRoot := findRepoRoot(t)
		cmd := exec.Command("go", "build", "-o", out, "./testdata/mcp-fixtures/stub-server")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if err := cmd.Run(); err != nil {
			stubBinErr = err
			return
		}
		stubBinPath = out
	})
	if stubBinErr != nil {
		t.Fatalf("build stub: %v", stubBinErr)
	}
	if stubBinPath == "" {
		t.Skip("stub not built")
	}
	return stubBinPath
}

// findRepoRoot walks up from the current working directory until it finds go.mod.
func findRepoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above cwd")
		}
		dir = parent
	}
}

func TestClient_InitializeAndListTools(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))
	tools, err := c.listTools(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)
}

func TestClient_CallTool_Success(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))

	result, isErr, err := c.callTool(ctx, "echo", json.RawMessage(`{"a":1}`))
	require.NoError(t, err)
	assert.False(t, isErr)
	assert.Contains(t, string(result), "ok")
}

func TestClient_CallTool_ServerError(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MCP_STUB_RETURN_ERROR=1")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, c.initialize(ctx))

	_, isErr, err := c.callTool(ctx, "echo", json.RawMessage(`{}`))
	require.NoError(t, err) // tool-level errors are not Go errors
	assert.True(t, isErr)
}

func TestClient_InitializeFail_ServerExits(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MCP_STUB_EXIT_ON_INIT=1")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	c := newClient(stdout, stdin)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.Error(t, c.initialize(ctx))
}
```

- [ ] **Step 2: Confirm failure (compile error)**

```bash
go test ./internal/mcp/ -run TestClient_ -v
```

Expected: `client` undefined.

- [ ] **Step 3: Implement the client**

Create `internal/mcp/client.go`:

```go
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// client is a single-connection MCP JSON-RPC client. It reads one line
// per response from stdout, writes one line per request to stdin. Requests
// are correlated by ID.
type client struct {
	w    io.Writer
	r    *bufio.Reader
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan jsonrpcResponse
	closed  bool

	done chan struct{}
}

func newClient(stdout io.Reader, stdin io.Writer) *client {
	c := &client{
		w:       stdin,
		r:       bufio.NewReader(stdout),
		pending: map[int64]chan jsonrpcResponse{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *client) readLoop() {
	defer close(c.done)
	for {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			c.mu.Lock()
			c.closed = true
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		var idInt int64
		if err := json.Unmarshal(resp.ID, &idInt); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[idInt]
		delete(c.pending, idInt)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *client) send(ctx context.Context, method string, params any) (jsonrpcResponse, error) {
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return jsonrpcResponse{}, fmt.Errorf("marshal params: %w", err)
		}
		paramsRaw = b
	}
	reqBytes, _ := json.Marshal(jsonrpcRequest{
		JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw,
	})
	reqBytes = append(reqBytes, '\n')

	ch := make(chan jsonrpcResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return jsonrpcResponse{}, fmt.Errorf("mcp client closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if _, err := c.w.Write(reqBytes); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return jsonrpcResponse{}, fmt.Errorf("write: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return jsonrpcResponse{}, fmt.Errorf("mcp client closed while awaiting response")
		}
		return resp, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return jsonrpcResponse{}, ctx.Err()
	case <-c.done:
		return jsonrpcResponse{}, fmt.Errorf("mcp client stream ended")
	}
}

func (c *client) initialize(ctx context.Context) error {
	resp, err := c.send(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      map[string]string{"name": "cronfoundry", "version": "0.1"},
	})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize: %s (%d)", resp.Error.Message, resp.Error.Code)
	}
	return nil
}

func (c *client) listTools(ctx context.Context) ([]Tool, error) {
	resp, err := c.send(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list: %s", resp.Error.Message)
	}
	var out listToolsResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return nil, fmt.Errorf("tools/list result: %w", err)
	}
	tools := make([]Tool, 0, len(out.Tools))
	for _, t := range out.Tools {
		tools = append(tools, Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return tools, nil
}

// callTool returns (rawResult, isError, err). isError is true when the MCP
// server returned an error at the tool level (the LLM should see it); err
// is set only for transport / protocol failures.
func (c *client) callTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, bool, error) {
	resp, err := c.send(ctx, "tools/call", callToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, false, err
	}
	if resp.Error != nil {
		// JSON-RPC-level error → tool-level error (spec semantics).
		raw, _ := json.Marshal(map[string]any{
			"error": resp.Error.Message,
			"code":  resp.Error.Code,
		})
		return raw, true, nil
	}
	return resp.Result, false, nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/mcp/ -run TestClient_ -v
```

Expected: all 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/client.go internal/mcp/client_test.go
git commit -m "feat(mcp): single-server JSON-RPC stdio client"
```

---

## Task 9: Implement `internal/mcp/process.go` — subprocess lifecycle

**Files:**
- Create: `internal/mcp/process.go`

`process.go` wraps `exec.Cmd` with graceful shutdown semantics used by Manager.

- [ ] **Step 1: Write the file**

```go
package mcp

import (
	"io"
	"os/exec"
	"syscall"
	"time"
)

// shutdownGracePeriod bounds how long we wait for SIGTERM before SIGKILL.
// Exported for tests and for alignment with the spec's MCPShutdownGracePeriod.
const shutdownGracePeriod = 5 * time.Second

// serverProcess bundles an exec.Cmd with its stdio pipes for client use.
type serverProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func startServerProcess(command string, args, env []string) (*serverProcess, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = env
	// SIGTERM is sent to the process group to catch Node/Python children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &serverProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

// shutdown first closes stdin (many MCP servers exit cleanly on EOF),
// then SIGTERMs the process group, waits up to shutdownGracePeriod, and
// SIGKILLs if needed. Returns the exit code (or -1 if forced).
func (sp *serverProcess) shutdown() int {
	_ = sp.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- sp.cmd.Wait() }()

	select {
	case <-done:
		return sp.cmd.ProcessState.ExitCode()
	case <-time.After(200 * time.Millisecond):
		// Most well-behaved servers exit on stdin EOF; give them a hair of
		// additional time before escalating.
	}

	// SIGTERM the process group.
	if pid := sp.cmd.Process.Pid; pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
	select {
	case <-done:
		return sp.cmd.ProcessState.ExitCode()
	case <-time.After(shutdownGracePeriod):
	}

	// SIGKILL.
	if pid := sp.cmd.Process.Pid; pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	<-done
	return -1
}
```

- [ ] **Step 2: `go build ./...`**

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/process.go
git commit -m "feat(mcp): subprocess lifecycle with graceful shutdown"
```

Note: `process.go` is covered indirectly by Manager tests in Task 10; no dedicated test here.

---

## Task 10: Implement `internal/mcp/manager.go` + tests

**Files:**
- Create: `internal/mcp/manager.go`
- Create: `internal/mcp/manager_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/manager_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubServerEnv constructs a clean env for spawning the stub with the given
// behavior overrides.
func stubServerEnv(overrides map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

func TestManager_StartAndTools(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)

	mgr := NewManager(context.Background())
	defer mgr.Shutdown()

	require.NoError(t, mgr.Start("echo-server", bin, nil, stubServerEnv(nil)))
	tools := mgr.Tools("echo-server")
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)
}

func TestManager_DispatchAll_Success(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	require.NoError(t, mgr.Start("s", bin, nil, stubServerEnv(nil)))

	calls := []ToolUse{
		{ID: "1", Name: "s__echo", Input: json.RawMessage(`{"x":1}`)},
		{ID: "2", Name: "s__echo", Input: json.RawMessage(`{"y":2}`)},
	}
	results, fatal := mgr.DispatchAll(context.Background(), calls, 2*time.Second)
	require.Nil(t, fatal)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.False(t, r.IsError)
	}
}

func TestManager_DispatchAll_ToolLevelError(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	require.NoError(t, mgr.Start("s", bin, nil, stubServerEnv(map[string]string{
		"MCP_STUB_RETURN_ERROR": "1",
	})))

	calls := []ToolUse{{ID: "1", Name: "s__echo", Input: json.RawMessage(`{}`)}}
	results, fatal := mgr.DispatchAll(context.Background(), calls, 2*time.Second)
	require.Nil(t, fatal, "tool-level error is NOT a fatal error")
	require.Len(t, results, 1)
	assert.True(t, results[0].IsError)
}

func TestManager_DispatchAll_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	require.NoError(t, mgr.Start("s", bin, nil, stubServerEnv(map[string]string{
		"MCP_STUB_SLEEP_MS": "5000",
	})))

	calls := []ToolUse{{ID: "1", Name: "s__echo", Input: json.RawMessage(`{}`)}}
	results, fatal := mgr.DispatchAll(context.Background(), calls, 100*time.Millisecond)
	_ = results // not examined on fatal
	require.NotNil(t, fatal)
	assert.Equal(t, "mcp_tool_timeout", fatal.Kind)
}

func TestManager_Start_InitializeFails(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := stub(t)
	mgr := NewManager(context.Background())
	defer mgr.Shutdown()
	err := mgr.Start("s", bin, nil, stubServerEnv(map[string]string{
		"MCP_STUB_EXIT_ON_INIT": "1",
	}))
	require.Error(t, err)
}
```

Also, move the `ToolUse` type alias — since the manager API needs it and it's defined in `internal/llm` (Phase 3), create a *local* definition in `internal/mcp/manager.go` and convert in the runner. Rationale: avoid `mcp → llm` dependency (inversion would make llm testing import mcp, which is cleaner to avoid). The runner does the conversion.

- [ ] **Step 2: Implement `internal/mcp/manager.go`**

Create `internal/mcp/manager.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ToolUse is the subset of an LLM-emitted tool call that Manager needs to
// dispatch. Mirrors internal/llm.ToolUse, kept local to avoid a dependency
// cycle. The runner converts between the two.
type ToolUse struct {
	ID    string
	Name  string          // namespaced "<server>__<tool>"
	Input json.RawMessage
}

// CallResult is the outcome of a single tool dispatch.
type CallResult struct {
	ID         string
	ResultJSON json.RawMessage
	IsError    bool
	DurationMS int64
}

// FatalError is a failure that the run cannot recover from (server crash,
// per-call timeout, etc.). The runner treats this as a fatal run failure
// and fails with the given Kind.
type FatalError struct {
	Kind string // "mcp_server_crashed" | "mcp_tool_timeout"
	Err  error
}

func (e *FatalError) Error() string { return e.Kind + ": " + e.Err.Error() }

// Manager owns one-or-more MCP servers for the lifetime of a single run.
type Manager struct {
	ctx context.Context

	mu      sync.Mutex
	servers map[string]*serverEntry
}

type serverEntry struct {
	name   string
	proc   *serverProcess
	client *client
	tools  []Tool
}

func NewManager(ctx context.Context) *Manager {
	return &Manager{
		ctx:     ctx,
		servers: map[string]*serverEntry{},
	}
}

// Start launches one server. Blocks until initialize + tools/list succeed,
// the process exits, or ctx deadline hits.
func (m *Manager) Start(name, command string, args, env []string) error {
	proc, err := startServerProcess(command, args, env)
	if err != nil {
		return fmt.Errorf("mcp: start %q: %w", name, err)
	}

	// Drain stderr into memory (last ~1KB) for crash diagnostics; keep the
	// goroutine running for the server's lifetime.
	// (Implementer: simple "read-and-discard-but-keep-last-N-bytes" ring buffer.
	// Small enough that we keep it inline here.)
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := proc.stderr.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	c := newClient(proc.stdout, proc.stdin)
	initCtx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		proc.shutdown()
		return fmt.Errorf("mcp: initialize %q: %w", name, err)
	}
	tools, err := c.listTools(initCtx)
	if err != nil {
		proc.shutdown()
		return fmt.Errorf("mcp: tools/list %q: %w", name, err)
	}

	m.mu.Lock()
	m.servers[name] = &serverEntry{name: name, proc: proc, client: c, tools: tools}
	m.mu.Unlock()
	return nil
}

// Tools returns the read-only tool list for a named server.
func (m *Manager) Tools(name string) []Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.servers[name]
	if s == nil {
		return nil
	}
	out := make([]Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// DispatchAll runs all calls in parallel, each bounded by perToolTimeout.
// Returns per-call results and an optional FatalError describing the first
// fatal condition observed.
func (m *Manager) DispatchAll(ctx context.Context, calls []ToolUse, perToolTimeout time.Duration) ([]CallResult, *FatalError) {
	results := make([]CallResult, len(calls))
	var (
		fatalMu sync.Mutex
		fatal   *FatalError
	)

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call ToolUse) {
			defer wg.Done()
			server, tool, ok := splitToolName(call.Name)
			if !ok {
				fatalMu.Lock()
				if fatal == nil {
					fatal = &FatalError{Kind: "mcp_tool_invalid_name", Err: fmt.Errorf("tool name %q missing __ namespace", call.Name)}
				}
				fatalMu.Unlock()
				return
			}
			m.mu.Lock()
			entry := m.servers[server]
			m.mu.Unlock()
			if entry == nil {
				fatalMu.Lock()
				if fatal == nil {
					fatal = &FatalError{Kind: "mcp_tool_invalid_name", Err: fmt.Errorf("no such server %q", server)}
				}
				fatalMu.Unlock()
				return
			}

			start := time.Now()
			callCtx, cancel := context.WithTimeout(ctx, perToolTimeout)
			defer cancel()
			raw, isErr, err := entry.client.callTool(callCtx, tool, call.Input)
			dur := time.Since(start).Milliseconds()
			if err != nil {
				kind := "mcp_server_crashed"
				if errors.Is(err, context.DeadlineExceeded) {
					kind = "mcp_tool_timeout"
				}
				fatalMu.Lock()
				if fatal == nil {
					fatal = &FatalError{Kind: kind, Err: err}
				}
				fatalMu.Unlock()
				return
			}
			results[i] = CallResult{ID: call.ID, ResultJSON: raw, IsError: isErr, DurationMS: dur}
		}(i, call)
	}
	wg.Wait()

	return results, fatal
}

// Shutdown terminates every server. Idempotent.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	servers := m.servers
	m.servers = map[string]*serverEntry{}
	m.mu.Unlock()
	for _, s := range servers {
		_ = s.proc.shutdown()
	}
}

// splitToolName splits "<server>__<tool>" into (server, tool, true) or
// returns (_, _, false) on malformed input.
func splitToolName(name string) (string, string, bool) {
	i := strings.Index(name, "__")
	if i <= 0 || i == len(name)-2 {
		return "", "", false
	}
	return name[:i], name[i+2:], true
}
```

- [ ] **Step 3: Run the tests**

```bash
go test ./internal/mcp/ -v -timeout 60s
```

Expected: all Manager tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/manager.go internal/mcp/manager_test.go
git commit -m "feat(mcp): multi-server manager with parallel dispatch"
```

---

# Phase 3 — LLM provider extension

## Task 11: Extend `internal/llm/provider.go` with tool types

**Files:**
- Modify: `internal/llm/provider.go`

- [ ] **Step 1: Extend types**

Append (or place appropriately among existing types):

```go
// Role additions for tool use.
const (
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message extensions:
//   - RoleAssistant messages may carry ToolUses (the model called tools)
//   - RoleTool messages MUST set ToolUseID (the tool call they answer)
// These fields are ignored for other roles. Kept on the same struct so the
// single message type handles both non-tool and tool conversations.
type ToolUse struct {
	ID    string          // provider-assigned
	Name  string          // namespaced "<server>__<tool>"
	Input json.RawMessage // arbitrary JSON, passed verbatim to MCP tools/call
}

type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// TurnResult is one tool-aware turn's output. The runner appends an
// assistant message with Text + ToolUses, dispatches tool calls if any,
// appends tool-role messages with results, and calls ChatTurn again.
type TurnResult struct {
	Text       string
	ToolUses   []ToolUse
	Usage      Usage
	StopReason string // "end_turn" | "tool_use" | "max_tokens" | ...
}

// ToolCapableProvider is implemented by providers that support a single
// tool-aware completion turn. Non-tool skills keep using Chat.
type ToolCapableProvider interface {
	Provider
	ChatTurn(
		ctx context.Context,
		messages []Message,
		tools []ToolDef,
		opts CallOptions,
		onChunk func(StreamChunk),
	) (TurnResult, error)
}
```

Update `Message`:

```go
type Message struct {
	Role      Role
	Content   string
	ToolUses  []ToolUse // RoleAssistant: tool_use blocks the model emitted
	ToolUseID string    // RoleTool: id of the call this message answers
}
```

Add the `json` import at the top of the file (`"encoding/json"`).

- [ ] **Step 2: `go build ./...`**

Expected: PASS. Existing non-tool callers don't touch the new fields.

- [ ] **Step 3: Commit**

```bash
git add internal/llm/provider.go
git commit -m "feat(llm): add tool-use types and ToolCapableProvider interface"
```

---

## Task 12: Implement `ChatTurn` for Anthropic

**Files:**
- Modify: `internal/llm/anthropic.go`
- Modify: `internal/llm/anthropic_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/llm/anthropic_test.go`:

```go
func TestAnthropic_ChatTurn_ReturnsToolUses(t *testing.T) {
	// Recorded Anthropic SDK response with one tool_use block.
	// Fixture is a minimal HTTP server that returns the recorded SSE stream.
	fixture := startAnthropicFixture(t, fixtureAnthropicToolUse)
	defer fixture.Close()

	p := NewAnthropic(fixture.URL)
	tcp, ok := p.(ToolCapableProvider)
	require.True(t, ok)

	tools := []ToolDef{{
		Name:        "get_weather",
		Description: "return current weather",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}}
	tr, err := tcp.ChatTurn(
		context.Background(),
		[]Message{{Role: RoleUser, Content: "what's the weather in SF?"}},
		tools,
		CallOptions{Model: "claude-opus-4-7", APIKey: "test", MaxTokens: 1024},
		func(StreamChunk) {},
	)
	require.NoError(t, err)
	require.Len(t, tr.ToolUses, 1)
	assert.Equal(t, "get_weather", tr.ToolUses[0].Name)
	assert.Equal(t, "tool_use", tr.StopReason)
	assert.Positive(t, tr.Usage.OutputTokens)
}

func TestAnthropic_ChatTurn_TextOnly(t *testing.T) {
	fixture := startAnthropicFixture(t, fixtureAnthropicTextOnly)
	defer fixture.Close()

	p := NewAnthropic(fixture.URL).(ToolCapableProvider)
	tr, err := p.ChatTurn(
		context.Background(),
		[]Message{{Role: RoleUser, Content: "hi"}},
		nil,
		CallOptions{Model: "claude-opus-4-7", APIKey: "test", MaxTokens: 100},
		func(StreamChunk) {},
	)
	require.NoError(t, err)
	assert.Empty(t, tr.ToolUses)
	assert.Equal(t, "end_turn", tr.StopReason)
	assert.NotEmpty(t, tr.Text)
}
```

`startAnthropicFixture` + fixture constants go into a separate `anthropic_fixture_test.go` helper, built by hand from a recorded Anthropic streaming-messages SSE response. Capture by running once against the live API with `-httptest.serve` or `go run` a recorder; paste the exact bytes as a Go raw-string literal. This is a one-time manual step. Commit the fixture strings alongside the test.

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/llm/ -run TestAnthropic_ChatTurn -v
```

Expected: `ToolCapableProvider` type assertion fails (anthropicProvider doesn't implement ChatTurn yet).

- [ ] **Step 3: Implement `ChatTurn`**

Append to `internal/llm/anthropic.go`:

```go
// ChatTurn performs a single tool-aware turn against Anthropic's Messages API.
// Multi-turn orchestration is the caller's responsibility; this method returns
// once the model emits end_turn / tool_use / max_tokens, whichever comes first.
func (p *anthropicProvider) ChatTurn(
	ctx context.Context,
	messages []Message,
	tools []ToolDef,
	opts CallOptions,
	onChunk func(StreamChunk),
) (TurnResult, error) {
	clientOpts := []option.RequestOption{
		option.WithAPIKey(opts.APIKey),
		option.WithMaxRetries(3),
	}
	if p.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(p.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)

	// Build SYSTEM and MESSAGES from the role-shaped input.
	var system string
	var apiMsgs []anthropic.MessageParam
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case RoleUser:
			apiMsgs = append(apiMsgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tu := range m.ToolUses {
				blocks = append(blocks, anthropic.NewToolUseBlock(tu.ID, tu.Input, tu.Name))
			}
			apiMsgs = append(apiMsgs, anthropic.NewAssistantMessage(blocks...))
		case RoleTool:
			apiMsgs = append(apiMsgs, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolUseID, m.Content, false),
			))
		}
	}

	// Translate ToolDef → anthropic.ToolParam.
	var apiTools []anthropic.ToolUnionParam
	for _, t := range tools {
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{Raw: t.InputSchema},
			},
		})
	}

	maxTok := int64(opts.MaxTokens)
	if maxTok <= 0 {
		maxTok = 4096
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(opts.Model),
		MaxTokens: maxTok,
		Messages:  apiMsgs,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var result TurnResult
	// Accumulate per-block text and tool_use JSON.
	var textBuf, curToolID, curToolName string
	var curToolInputBuf []byte

	flushCurrentToolUse := func() {
		if curToolID == "" {
			return
		}
		result.ToolUses = append(result.ToolUses, ToolUse{
			ID: curToolID, Name: curToolName, Input: json.RawMessage(curToolInputBuf),
		})
		curToolID, curToolName, curToolInputBuf = "", "", nil
	}

	for stream.Next() {
		evt := stream.Current()
		switch v := evt.AsAny().(type) {
		case anthropic.MessageStartEvent:
			if v.Message.Usage.InputTokens > 0 {
				result.Usage.InputTokens = int(v.Message.Usage.InputTokens)
			}
		case anthropic.ContentBlockStartEvent:
			flushCurrentToolUse()
			if tu, ok := v.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
				curToolID = tu.ID
				curToolName = tu.Name
				curToolInputBuf = []byte{}
			}
		case anthropic.ContentBlockDeltaEvent:
			switch d := v.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if d.Text != "" {
					textBuf += d.Text
					onChunk(StreamChunk{Delta: d.Text})
				}
			case anthropic.InputJSONDelta:
				curToolInputBuf = append(curToolInputBuf, d.PartialJSON...)
			}
		case anthropic.ContentBlockStopEvent:
			flushCurrentToolUse()
		case anthropic.MessageDeltaEvent:
			if v.Usage.OutputTokens > 0 {
				result.Usage.OutputTokens = int(v.Usage.OutputTokens)
			}
			if v.Delta.StopReason != "" {
				result.StopReason = string(v.Delta.StopReason)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return result, fmt.Errorf("anthropic chat_turn: %w", err)
	}
	flushCurrentToolUse()
	result.Text = textBuf
	return result, nil
}
```

Note: the exact SDK field names (`anthropic.NewToolUseBlock`, `anthropic.NewToolResultBlock`, `anthropic.ToolInputSchemaParam`, event types like `ContentBlockStartEvent`, delta types like `InputJSONDelta`) may differ slightly by SDK version. Cross-check against the installed `anthropic-sdk-go` version; adjust names to match.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/llm/ -run TestAnthropic_ChatTurn -v
```

Expected: PASS once fixture constants are in place.

- [ ] **Step 5: Commit**

```bash
git add internal/llm/anthropic.go internal/llm/anthropic_test.go internal/llm/anthropic_fixture_test.go
git commit -m "feat(llm): Anthropic ChatTurn with tool-use support"
```

---

## Task 13: Implement `ChatTurn` for OpenAI

**Files:**
- Modify: `internal/llm/openai.go`
- Modify: `internal/llm/openai_test.go`

Structurally identical to Task 12 but translates against the OpenAI Chat Completions API. Key differences:

- Tools are `tools=[{type:"function", function:{name, description, parameters}}]`.
- Tool calls land in `assistant.tool_calls=[{id, function:{name, arguments}}]`.
- Tool responses go back as `messages:[{role:"tool", tool_call_id, content}]`.
- Streaming deltas for tool-call JSON arrive incrementally as `delta.tool_calls[i].function.arguments` fragments.

- [ ] **Step 1: Write the failing tests**

Mirror Task 12's two tests (`TestOpenAI_ChatTurn_ReturnsToolCalls`, `TestOpenAI_ChatTurn_TextOnly`). Use recorded OpenAI SSE fixtures in `openai_fixture_test.go`.

- [ ] **Step 2: Implement `ChatTurn` in `internal/llm/openai.go`**

Follow the same pattern as the Anthropic implementation. The general shape:

```go
func (p *openaiProvider) ChatTurn(
	ctx context.Context,
	messages []Message,
	tools []ToolDef,
	opts CallOptions,
	onChunk func(StreamChunk),
) (TurnResult, error) {
	// 1) Build openai.ChatCompletionMessageParamUnion list from messages.
	// 2) Build openai.ChatCompletionToolParam list from tools.
	// 3) Open streaming completion.
	// 4) Accumulate: text from choice.delta.content; tool_calls keyed by
	//    index, stitching function.arguments fragments.
	// 5) On stop, build TurnResult: Text, ToolUses, Usage (from the
	//    final chunk's Usage field), StopReason (from finish_reason).
	// 6) Return.
}
```

The `openai-go` SDK emits discrete tool-call deltas; accumulate `arguments` into a per-index `[]byte` and finalize on stream end. Each accumulated index becomes one `ToolUse{ID, Name, Input}`.

For the exact SDK types, grep the existing `internal/llm/openai.go` to find the types / packages in use; the ChatTurn implementation reuses the same types to reduce surprise.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/llm/ -run TestOpenAI_ChatTurn -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/llm/openai.go internal/llm/openai_test.go internal/llm/openai_fixture_test.go
git commit -m "feat(llm): OpenAI ChatTurn with tool-use support"
```

---

# Phase 4 — Runner integration

## Task 14: Extend `/internal/runs/:id/context` to include MCP config

**Files:**
- Modify: `internal/db/queries/run.sql`
- Modify: `internal/db/gen/run.sql.go` (regenerated)
- Modify: `internal/api/run_context.go`
- Modify: `internal/api/run_context_test.go`

The runner fetches its config from `/internal/runs/:id/context` at startup. That endpoint must now surface `mcp_servers` (from the skill's frontmatter), `mcp_env` (from the schedule), and `max_turns` (the resolved effective value).

- [ ] **Step 1: Extend `GetRunForContext` in `internal/db/queries/run.sql`**

Find the existing `GetRunForContext` block and add the three new columns to the SELECT list:

```sql
-- name: GetRunForContext :one
SELECT ...existing columns...,
       s.mcp_env_json,
       s.max_turns,
       sk.frontmatter_json
FROM run r
JOIN schedule s ON s.id = r.schedule_id
JOIN skill    sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;
```

(If the query already joins skill but doesn't select `frontmatter_json`, add it.)

- [ ] **Step 2: Regenerate sqlc**

```bash
make sqlc
```

- [ ] **Step 3: Update the API response shape**

In `internal/api/run_context.go`, add fields to the response:

```go
type runContextResponse struct {
	// ...existing fields...
	MCPServers []config.MCPServer                        `json:"mcp_servers,omitempty"`
	MCPEnv     map[string]map[string]runContextSecretRef `json:"mcp_env,omitempty"`
	MaxTurns   int                                       `json:"max_turns,omitempty"`
}
```

Where `runContextSecretRef` is the existing type used for env-secret references (reuse; don't duplicate).

Populate from the row:

```go
// Parse frontmatter JSON to get MCPServers.
var fm config.SkillFrontmatter
if err := json.Unmarshal(row.FrontmatterJson, &fm); err != nil {
	// log & continue; MCP support is opt-in, missing frontmatter shouldn't 500 the run
}
resp.MCPServers = fm.MCPServers

// Parse mcp_env_json into the response shape (secret refs pass through verbatim).
if len(row.McpEnvJson) > 0 && string(row.McpEnvJson) != "{}" {
	var mcpEnv map[string]map[string]config.EnvValue
	if err := json.Unmarshal(row.McpEnvJson, &mcpEnv); err == nil {
		resp.MCPEnv = convertMCPEnv(mcpEnv)
	}
}

// Resolve effective max_turns: schedule → skill frontmatter → 0 (runner falls back to its default).
switch {
case row.MaxTurns != nil && *row.MaxTurns > 0:
	resp.MaxTurns = int(*row.MaxTurns)
case fm.MaxTurns > 0:
	resp.MaxTurns = fm.MaxTurns
}
```

`convertMCPEnv` mirrors the existing env-conversion helper (swap literal vs. secret-ref shape).

- [ ] **Step 4: Extend the test**

In `internal/api/run_context_test.go`, extend the existing "happy-path" test (whatever is closest to a minimal run-with-fields case) to:

1. Seed the schedule with `mcp_env_json = '{"github":{"GITHUB_PAT":{"secret":"github_mcp_pat"}}}'::jsonb` and `max_turns = 40`.
2. Seed the skill with `frontmatter_json = '{"mcp_servers":[{"name":"github","command":"npx","args":["-y","..."]}]}'::jsonb`.
3. Call `GET /internal/runs/:id/context`.
4. Assert `resp.MCPServers[0].Name == "github"`, `resp.MaxTurns == 40`, `resp.MCPEnv["github"]["GITHUB_PAT"]` resolves to the expected shape.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/api/ -run RunContext -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/run.sql internal/db/gen/ internal/api/run_context.go internal/api/run_context_test.go
git commit -m "feat(api): expose mcp_servers, mcp_env, max_turns in run context"
```

---

## Task 15: Refactor `internal/runner/runner.go` into three-phase orchestration

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`

This is the biggest single code change. Work in two sub-steps: (a) introduce the phases but keep the non-tool path byte-identical, (b) add the tool path.

- [ ] **Step 1: Add `RunInput` fields for MCP config**

```go
type RunInput struct {
	// ...existing...
	MCPServers []config.MCPServer
	MCPEnv     map[string]map[string]config.EnvValue
	MaxTurns   int // 0 = use skill frontmatter or DefaultMaxTurns
}
```

- [ ] **Step 2: Add `Deps.MCPManagerFactory` for test injection**

```go
type Deps struct {
	// ...existing...
	MCPManagerFactory func(ctx context.Context) mcpManager
}
```

Where `mcpManager` is the small subset of `mcp.Manager` that the runner uses, declared as an interface in the runner package (so tests pass a fake):

```go
type mcpManager interface {
	Start(name, command string, args, env []string) error
	Tools(name string) []mcp.Tool
	DispatchAll(ctx context.Context, calls []mcp.ToolUse, perToolTimeout time.Duration) ([]mcp.CallResult, *mcp.FatalError)
	Shutdown()
}
```

Default factory in `New()`: `d.MCPManagerFactory = func(ctx context.Context) mcpManager { return mcp.NewManager(ctx) }`.

- [ ] **Step 3: Add test doubles and the first failing tool-path test**

At the top of `internal/runner/runner_test.go`, add two test doubles:

```go
// fakeToolProvider is a scripted ToolCapableProvider: each call to ChatTurn
// returns the next TurnResult from the queue (round-robin if exhausted).
type fakeToolProvider struct {
	turns []llm.TurnResult
	i     int
}

func (f *fakeToolProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	return llm.Usage{}, fmt.Errorf("fakeToolProvider does not implement Chat")
}
func (f *fakeToolProvider) ChatTurn(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.TurnResult, error) {
	if len(f.turns) == 0 {
		return llm.TurnResult{}, fmt.Errorf("no turns scripted")
	}
	tr := f.turns[f.i%len(f.turns)]
	f.i++
	return tr, nil
}

// fakeMCPManager is a scripted mcpManager. Start is recorded; DispatchAll
// returns canned results. Shutdown is counted so tests can assert cleanup.
type fakeMCPManager struct {
	startErr      error
	tools         map[string][]mcp.Tool
	dispatch      func(calls []mcp.ToolUse) ([]mcp.CallResult, *mcp.FatalError)
	shutdownCalls int
	startedEnvs   map[string][]string // name → env passed to Start
}

func (f *fakeMCPManager) Start(name, command string, args, env []string) error {
	if f.startErr != nil {
		return f.startErr
	}
	if f.startedEnvs == nil {
		f.startedEnvs = map[string][]string{}
	}
	f.startedEnvs[name] = env
	return nil
}
func (f *fakeMCPManager) Tools(name string) []mcp.Tool { return f.tools[name] }
func (f *fakeMCPManager) DispatchAll(ctx context.Context, calls []mcp.ToolUse, _ time.Duration) ([]mcp.CallResult, *mcp.FatalError) {
	return f.dispatch(calls)
}
func (f *fakeMCPManager) Shutdown() { f.shutdownCalls++ }
```

Now add the first tool-path test:

```go
func TestRunner_ToolPath_HappyOneToolThenEndTurn(t *testing.T) {
	fake := &fakeToolProvider{turns: []llm.TurnResult{
		// Turn 1: model calls one tool.
		{
			Text: "",
			ToolUses: []llm.ToolUse{{
				ID: "tool_1", Name: "stub__echo", Input: json.RawMessage(`{"q":"hi"}`),
			}},
			Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
			StopReason: "tool_use",
		},
		// Turn 2: model emits final text.
		{
			Text:       "Final answer.",
			Usage:      llm.Usage{InputTokens: 20, OutputTokens: 8},
			StopReason: "end_turn",
		},
	}}
	mgr := &fakeMCPManager{
		tools: map[string][]mcp.Tool{"stub": {{Name: "echo", Description: "d"}}},
		dispatch: func(calls []mcp.ToolUse) ([]mcp.CallResult, *mcp.FatalError) {
			out := make([]mcp.CallResult, len(calls))
			for i, c := range calls {
				out[i] = mcp.CallResult{ID: c.ID, ResultJSON: json.RawMessage(`"ok"`)}
			}
			return out, nil
		},
	}

	repo, manifest := writeSkillRepo(t, map[string]string{
		"cronfoundry.yaml": `version: 1
skills:
  - path: skills/s
    schedules:
      - name: n
        cron: "* * * * *"
        provider: anthropic
        model: claude-opus-4-7
        mcp_env:
          stub: {}
`,
		"skills/s/SKILL.md": `---
name: s
mcp_servers:
  - name: stub
    command: /bin/true
---
Body.
`,
	})

	r := New(Deps{
		ProviderFactory:   func(string) (llm.Provider, error) { return fake, nil },
		Publishers:        map[string]publish.Publisher{}, // no destinations in test
		MCPManagerFactory: func(ctx context.Context) mcpManager { return mgr },
		Now:               time.Now,
	})
	result, err := r.Run(context.Background(), RunInput{
		RepoRoot:     repo,
		ManifestPath: manifest,
		SkillPath:    "skills/s",
		ScheduleName: "n",
		DryRun:       true,
		LLMAPIKey:    "test",
		MCPServers:   []config.MCPServer{{Name: "stub", Command: "/bin/true"}},
		MCPEnv:       map[string]map[string]config.EnvValue{"stub": {}},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.Equal(t, "Final answer.", result.Output)
	assert.EqualValues(t, 30, result.Usage.InputTokens)
	assert.EqualValues(t, 13, result.Usage.OutputTokens)
	assert.Equal(t, 1, mgr.shutdownCalls, "manager must be shut down exactly once")
	assert.Contains(t, mgr.startedEnvs, "stub")
}
```

Add the remaining four cases following the same harness pattern — each swaps out one detail:

- `TestRunner_ToolPath_MaxTurnsExceeded`: fakeToolProvider always returns `tool_use`; set `RunInput.MaxTurns = 3`; assert `result.Status == StatusFailed`, `result.ErrorKind == "max_turns_exceeded"`, `mgr.shutdownCalls == 1`.
- `TestRunner_ToolPath_ProviderNotToolCapable`: `ProviderFactory` returns a plain `fakeProvider` (implements `Chat` only); assert `result.ErrorKind == "provider_tool_unsupported"`, and `mgr.shutdownCalls == 0` (fail before starting any servers).
- `TestRunner_ToolPath_ServerStartFailure`: `fakeMCPManager{startErr: fmt.Errorf("boom")}`; assert `result.ErrorKind == "mcp_server_start_failed"`, `mgr.shutdownCalls == 1` (defer fires).
- `TestRunner_ToolPath_FatalDuringDispatch`: dispatch callback returns `&mcp.FatalError{Kind: "mcp_tool_timeout", Err: ...}`; assert `result.ErrorKind == "mcp_tool_timeout"`, shutdownCalls == 1.

`writeSkillRepo` already exists in the runner test package (search for it; if absent, extract the common fixture-building code from existing `TestRunner_*` cases into a helper).

- [ ] **Step 4: Implement the tool path**

In `internal/runner/runner.go`, wrap the existing LLM-call block in a conditional:

```go
if len(in.MCPServers) == 0 {
	// existing non-tool path — unchanged
	// call provider.Chat, stream to sb, parse memory, publish, writeback
} else {
	toolProvider, ok := provider.(llm.ToolCapableProvider)
	if !ok {
		return failWithKind(&result, "provider_tool_unsupported",
			fmt.Errorf("provider %s does not support MCP tool use", sch.Provider))
	}

	mgr := r.deps.MCPManagerFactory(ctx)
	defer mgr.Shutdown()

	for _, s := range in.MCPServers {
		env, err := resolveServerEnv(s.Name, in.MCPEnv, in.Secrets)
		if err != nil {
			return failWithKind(&result, "mcp_server_start_failed", err)
		}
		if err := mgr.Start(s.Name, s.Command, s.Args, env); err != nil {
			return failWithKind(&result, "mcp_server_start_failed", err)
		}
	}

	var tools []llm.ToolDef
	for _, s := range in.MCPServers {
		for _, t := range mgr.Tools(s.Name) {
			tools = append(tools, llm.ToolDef{
				Name:        s.Name + "__" + t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}

	messages := []llm.Message{}
	if envBanner != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: envBanner})
	}
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: body})

	maxTurns := resolveMaxTurns(in.MaxTurns, skill.Frontmatter.MaxTurns)
	perToolTimeout := time.Duration(DefaultMCPToolCallTimeoutS) * time.Second
	var finalOutput string

	for turn := 1; turn <= maxTurns; turn++ {
		tr, err := toolProvider.ChatTurn(ctx, messages, tools, llm.CallOptions{
			Model: sch.Model, MaxTokens: skill.Frontmatter.MaxTokens,
			APIKey: in.LLMAPIKey, Endpoint: in.LLMEndpoint, Deployment: in.LLMDeployment,
		}, func(c llm.StreamChunk) { /* live-tail as-is */ })
		if err != nil {
			return failWithKind(&result, "llm_error", err)
		}
		result.Usage.InputTokens += tr.Usage.InputTokens
		result.Usage.OutputTokens += tr.Usage.OutputTokens

		messages = append(messages, llm.Message{
			Role: llm.RoleAssistant, Content: tr.Text, ToolUses: tr.ToolUses,
		})
		if len(tr.ToolUses) == 0 {
			finalOutput = tr.Text
			break
		}
		mcpCalls := toMCPCalls(tr.ToolUses) // tiny helper: []llm.ToolUse → []mcp.ToolUse
		results, fatal := mgr.DispatchAll(ctx, mcpCalls, perToolTimeout)
		if fatal != nil {
			return failWithKind(&result, fatal.Kind, fatal.Err)
		}
		for _, r := range results {
			messages = append(messages, llm.Message{
				Role: llm.RoleTool, ToolUseID: r.ID, Content: string(r.ResultJSON),
			})
		}
	}
	if finalOutput == "" {
		return failWithKind(&result, "max_turns_exceeded",
			fmt.Errorf("exceeded %d turns", maxTurns))
	}
	result.CostCents = llm.CostCents(sch.Provider, sch.Model, result.Usage)

	// Fall through to the existing memory parse → publish → writeback path
	// using finalOutput as the LLM's text.
	// Reorganize the existing code so both paths share the post-LLM code.
}
```

Add at the top of the file:

```go
const (
	DefaultMaxTurns            = 20
	DefaultMCPToolCallTimeoutS = 60
)

func resolveMaxTurns(fromSchedule, fromSkill int) int {
	if fromSchedule > 0 {
		return fromSchedule
	}
	if fromSkill > 0 {
		return fromSkill
	}
	return DefaultMaxTurns
}

func resolveServerEnv(name string, mcpEnv map[string]map[string]config.EnvValue, s *secrets.Resolver) ([]string, error) {
	var env []string
	for k, v := range mcpEnv[name] {
		if v.Secret != "" {
			val, err := s.Get(v.Secret)
			if err != nil {
				return nil, fmt.Errorf("mcp_env %s.%s: %w", name, k, err)
			}
			env = append(env, k+"="+val)
		} else {
			env = append(env, k+"="+v.Literal)
		}
	}
	return env, nil
}

func toMCPCalls(in []llm.ToolUse) []mcp.ToolUse {
	out := make([]mcp.ToolUse, len(in))
	for i, t := range in {
		out[i] = mcp.ToolUse{ID: t.ID, Name: t.Name, Input: t.Input}
	}
	return out
}
```

And introduce `failWithKind` as a small wrapper if not already present (it is not in the current code; extract the fail pattern):

```go
func failWithKind(r *RunResult, kind string, err error) (RunResult, error) {
	r.Status = StatusFailed
	// If RunResult doesn't currently have ErrorKind, add it as a new field.
	r.ErrorKind = kind
	r.FinishedAt = time.Now()
	return *r, err
}
```

Add `ErrorKind string` to `RunResult` if absent. The production runner wiring in `cmd/cronfoundry/runner.go` should forward `RunResult.ErrorKind` into the `finalizeRequest`'s `error_kind` field.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/runner/ -v
```

Expected: existing tests PASS, new tool-path tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/runner.go internal/runner/runner_test.go
git commit -m "feat(runner): three-phase tool-aware execution path

Adds a parallel orchestration path for skills that declare mcp_servers:
launch servers, aggregate tools, run a multi-turn LLM loop via ChatTurn,
dispatch tool calls through internal/mcp.Manager, feed results back as
tool-role messages, terminate when the LLM emits no tool_use or a bound
is hit. Non-tool skills continue through the original single-shot path."
```

---

## Task 16: Wire the production runner (`cmd/cronfoundry/runner.go`) to forward MCP config

**Files:**
- Modify: `cmd/cronfoundry/runner.go`
- Modify: `cmd/cronfoundry/runner_test.go`

The production runner reads `runContextResponse` from `/internal/runs/:id/context` and invokes `internal/runner.Runner.Run`. It needs to pass the new MCP fields through.

- [ ] **Step 1: Extend the context-response parsing**

Wherever the runner decodes `runContextResponse` (search for the type alias / response decode), surface `MCPServers`, `MCPEnv`, `MaxTurns` into local variables.

- [ ] **Step 2: Extend the `RunInput` construction**

```go
in := runner.RunInput{
	// ...existing fields...
	MCPServers: resp.MCPServers,
	MCPEnv:     resp.MCPEnv,   // convert from API-resp shape to config.EnvValue map
	MaxTurns:   resp.MaxTurns,
}
```

You may need a small helper `apiMCPEnvToConfig(map[string]map[string]runContextSecretRef) map[string]map[string]config.EnvValue`.

- [ ] **Step 3: Forward `RunResult.ErrorKind` into the finalize body**

In the existing finalize POST, set `error_kind` from `result.ErrorKind` when non-empty. Currently the runner already sets error_kind for some failure modes — extend the set.

- [ ] **Step 4: Minimal runner_test.go update**

If existing tests mock the context endpoint, add the new JSON fields to the mock response. Confirm `go test ./cmd/cronfoundry/ -short` still passes.

- [ ] **Step 5: Commit**

```bash
git add cmd/cronfoundry/runner.go cmd/cronfoundry/runner_test.go
git commit -m "feat(runner-bin): forward mcp_servers/mcp_env/max_turns through to runner"
```

---

## Task 17: End-to-end test using the stub MCP server

**Files:**
- Modify: `cmd/cronfoundry/e2e_test.go`

- [ ] **Step 1: Extend `e2e_test.go`**

Add a new test case that:

1. Seeds a skill whose `frontmatter_json` declares one `mcp_servers` entry pointing at the stub binary (build path recorded in a `t.Setenv` or computed via the helper from Task 6).
2. Seeds a schedule with `mcp_env_json = '{"stub":{}}'`.
3. Provides a fake LLM that emits one tool_use (`stub__echo`) and, on the next turn, `end_turn` with a final `<memory>...</memory>` block.
4. Drives scheduler → runner → finalize through the existing test server.
5. Asserts `run.status = 'succeeded'`, tokens counted, memory committed (if writeback enabled), no orphan processes.

Because e2e_test.go already has substantial scaffolding, build the MCP case as a new `TestE2E_MCPToolLoop` sibling to existing `TestE2E_*` tests.

- [ ] **Step 2: Run**

```bash
go test ./cmd/cronfoundry/ -run TestE2E_MCPToolLoop -v -timeout 5m
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/cronfoundry/e2e_test.go
git commit -m "test(e2e): MCP tool loop against stub server"
```

---

# Phase 5 — UI + image + final gate

## Task 18: UI — surface MCP servers and turn count

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/pages/Runs.tsx`

- [ ] **Step 1: Extend `Schedule` and related TS types**

In `web/src/lib/types.ts`:

```typescript
export interface Schedule {
  // ...existing...
  max_turns: number | null
  mcp_servers: MCPServerRef[]  // derived from skill frontmatter on server side
}

export interface MCPServerRef {
  name: string
  command: string
  args: string[]
}
```

The schedule list endpoint must return `mcp_servers` alongside the existing fields. Extend `internal/webapi/schedules.go`'s list handler to include `mcp_servers` by reading and parsing the skill's `frontmatter_json` inside the same SELECT (reuse the existing JOIN against skill). Commit the backend change in this task too if not already covered.

- [ ] **Step 2: Dashboard tool icon**

In `web/src/pages/Dashboard.tsx`, add a small icon on cards where `mcp_servers.length > 0`:

```tsx
{s.mcp_servers && s.mcp_servers.length > 0 && (
  <span
    title={s.mcp_servers.map(m => m.name).join(', ')}
    className="text-xs px-1.5 py-0.5 rounded bg-indigo-900 text-indigo-200"
  >
    🔧 {s.mcp_servers.length}
  </span>
)}
```

- [ ] **Step 3: Runs page — per-turn rows in the event timeline**

The existing run-events timeline already renders `event_type` + `payload_json`. Adding new `event_type` values from the server (`mcp.turn.start`, `mcp.tool.call.start`, etc.) flows automatically — add a prettifier for tool-call rows:

```tsx
function formatMCPEvent(event_type: string, payload: any): string | null {
  if (event_type === 'mcp.turn.start') return `Turn ${payload?.turn}`
  if (event_type === 'mcp.tool.call.ok') return `${payload?.server}__${payload?.tool} · ${payload?.duration_ms}ms · ok`
  if (event_type === 'mcp.tool.call.fail') return `${payload?.server}__${payload?.tool} · error`
  if (event_type === 'mcp.tool.call.timeout') return `${payload?.server}__${payload?.tool} · timeout`
  if (event_type === 'mcp.server.start.ok') return `MCP server ${payload?.server} ready (${payload?.tool_count} tools)`
  return null
}
```

Integrate this into the run-detail timeline rendering (search for the existing switch/lookup that handles event_types).

- [ ] **Step 4: Build**

```bash
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/pages/Dashboard.tsx web/src/pages/Runs.tsx \
        internal/webapi/schedules.go  # if modified
git commit -m "feat(web): surface MCP servers and tool-call timeline"
```

Note: emitting the new `run_event` types from the runner itself is part of Phase 4's runner changes — they're written inside the tool loop right before / after each `mgr.DispatchAll` dispatch. If those emissions weren't added in Task 15, backfill them here and re-run runner tests.

---

## Task 19: Runner Dockerfile with Node + Python + uvx

**Files:**
- Create: `deploy/Dockerfile.runner`
- Modify: `deploy/Dockerfile` (API/scheduler only; remove runner binary)

- [ ] **Step 1: Create `deploy/Dockerfile.runner`**

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/runner ./cmd/runner

# Runtime image: Debian slim for apt-installable Node + Python + uvx.
FROM debian:12-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl nodejs npm python3 python3-pip git && \
    rm -rf /var/lib/apt/lists/*
RUN pip3 install --break-system-packages --no-cache-dir uv
# `uvx` is provided by the `uv` package.
COPY --from=build /out/runner /usr/local/bin/runner
# Non-root runtime.
RUN useradd --create-home --shell /usr/sbin/nologin runner
USER runner
WORKDIR /home/runner
ENTRYPOINT ["/usr/local/bin/runner"]
```

Node versions: `apt install nodejs` on Debian 12 currently provides Node 18+. If a newer Node is required by specific MCP servers, add a NodeSource repository step in the RUN line.

- [ ] **Step 2: Shrink `deploy/Dockerfile`**

Remove the runner binary from the main image (API + scheduler image stays distroless):

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/cronfoundry ./cmd/cronfoundry

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cronfoundry /cronfoundry
USER nonroot
ENTRYPOINT ["/cronfoundry"]
```

- [ ] **Step 3: Verify builds locally**

```bash
docker build -f deploy/Dockerfile -t cronfoundry-api-test .
docker build -f deploy/Dockerfile.runner -t cronfoundry-runner-test .
```

Expected: both PASS.

- [ ] **Step 4: Update CI / deploy docs to reference both image tags**

If `.github/workflows/*.yml` references a single Dockerfile, add a second build step for the runner. Update `docs/guides/smoke-test-mvp-azure.md` (or equivalent operator guide) to mention that the runner is now a separate image.

- [ ] **Step 5: Commit**

```bash
git add deploy/Dockerfile deploy/Dockerfile.runner .github/workflows/
git commit -m "build: split runner into its own image with Node/Python/uvx

The MCP feature lets skills launch arbitrary stdio MCP servers via
command/args. The default runner image now ships with Node 20 (via npm),
Python 3 with uv/uvx, and git, covering the bulk of the MCP ecosystem.
The API + scheduler image stays distroless-minimal (no runtime deps)."
```

---

## Task 20: Final gate — lint, vet, full tests, web build, manual smoke

**Files:** none

- [ ] **Step 1: `go vet`**

```bash
go vet ./...
```

- [ ] **Step 2: Lint**

```bash
make lint
```

- [ ] **Step 3: Full Go test suite (docker required for testcontainers)**

```bash
go test ./... -count=1 -timeout 15m
```

Expected: PASS.

- [ ] **Step 4: Web build**

```bash
cd web && npm run build
```

- [ ] **Step 5: Manual smoke (strongly recommended)**

Build the runner image locally and deploy to a scratch CronFoundry instance:

1. Register the runner image as the Container Apps Jobs image.
2. Configure a test skill with `mcp_servers: [{name: fetch, command: uvx, args: [mcp-server-fetch]}]`.
3. Configure the matching schedule with `mcp_env: { fetch: {} }` and Anthropic credentials.
4. Trigger manually; confirm run completes with 1–2 turns, the LLM's final text reaches destinations, and no lingering `uvx` processes remain in the Jobs container.

---

## Known interactions (not addressed in this plan)

- **Sync re-enables paused schedules.** Same caveat as the auto-pause plan: YAML pushes re-enable any schedule, which includes auto-paused ones. Not an MCP-specific issue, but worth noting because the same `UpsertSchedule` query is touched.
- **No Azure AI Foundry support.** Cross-validation rejects this combination at sync time; Foundry users must use OpenAI or Anthropic to access MCP.
- **No HTTP/SSE MCP transport.** All servers are stdio subprocesses co-located with the runner.
- **Runner image is bigger (~300MB).** Cold starts on Container Apps Jobs add ~2s. Operators who don't use MCP can stick with the old distroless runner by building without the MCP deps; provide that as a docs note if asked.
- **No observability for MCP tool arg/result bodies in the UI.** They flow into Log Analytics via the existing redaction pipeline; a dedicated inspector is deferred.

## Self-review checklist

- [ ] Spec's **Goals**: pipeline unchanged for non-tool skills, GitOps as source of truth, secret boundary preserved (mcp_env never banners), observability via run_event, auto-pause composes.
- [ ] Spec's **Non-Goals**: no HTTP/SSE transport, no server curation, no Azure Foundry, no tool filtering, no approval UI, no pooling, no tool args in destinations.
- [ ] Migration + sqlc + config + sync + validation tests all exist and pass.
- [ ] `internal/mcp` package tested against a real stub subprocess (not mocks), including crash, timeout, tool-level error, start failure, parallel dispatch.
- [ ] Provider tests use recorded SDK fixtures, cover tool_use and text-only outcomes, and multi-tool per turn (at least one case).
- [ ] Runner tests cover: happy one-turn, max-turns-exceeded, provider-not-tool-capable, server start failure, fatal during dispatch.
- [ ] E2E test drives the full stack with the stub server.
- [ ] Dashboard shows a tool icon; Runs page renders tool-call events.
- [ ] Runner Dockerfile ships Node + Python + uvx.
- [ ] All new `run.error_kind` values (`max_turns_exceeded`, `mcp_server_start_failed`, `mcp_server_crashed`, `mcp_tool_timeout`, `provider_tool_unsupported`) are set by a test case somewhere.
- [ ] No placeholder comments, no `fmt.Println`, no debug `slog.Debug` left in hot paths.
