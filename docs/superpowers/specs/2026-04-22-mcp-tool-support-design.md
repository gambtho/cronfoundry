# MCP Tool Support in Skills — Design

**Status:** Proposed
**Date:** 2026-04-22
**Author:** gambtho (brainstormed with Claude)
**Depends on:** MVP (`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`, deferred item #2)

## Overview

Skills declare stdio MCP servers in `SKILL.md` frontmatter. At run time, the runner launches each server as a subprocess inside the runner container, discovers its tools, advertises them to the LLM, and executes a multi-turn tool-use loop. Each turn: the LLM may emit tool calls; the runner dispatches them to the owning MCP server, collects results, and feeds them back as the next user turn. The loop ends when the LLM produces a final text response (no further tool calls), a bound is exceeded, or a server crashes.

The final text is treated exactly like today's one-shot output — parsed for `<memory>`, fed to destinations, written back. The rest of the pipeline (publishers, writeback, scheduler, auto-pause) is unchanged.

Provider support at MVP: **OpenAI and Anthropic only.** Azure AI Foundry is deferred because tool-call support varies across Foundry deployment models.

Safety bounds: wall-clock (existing `timeout_sec`), turn cap (`max_turns`, default 20), per-tool-call timeout (default 60s). Hitting any of them fails the run with a distinct `error_kind`; `max_turns_exceeded` and the other tool-specific failure kinds all count toward auto-pause (spec `2026-04-22-auto-pause-design.md`).

## Goals

- Let skills use MCP tools during their LLM call without changing the rest of the pipeline.
- Keep GitOps as source of truth — MCP server declarations live in `SKILL.md`, not the UI.
- Preserve the existing secret boundaries: MCP-server env flows through the Key Vault secret manifest, never through Postgres, never into the LLM prompt banner.
- First-class observability for tool calls: per-call `run_event` rows, live-tailable, redacted.
- Compose cleanly with auto-pause — tool-specific failures are `failed` runs and count toward the consecutive-failure streak.

## Non-Goals

- **No HTTP/SSE MCP transport.** stdio only. Remote servers deferred.
- **No MCP-server curation/allowlist.** Skills declare arbitrary commands; operators own their runner image.
- **No Azure AI Foundry support at MVP.** Deferred. Schedules using `provider: azure-foundry` with `mcp_servers:` declared fail validation at sync time.
- **No per-tool allow/deny filtering.** All of a declared server's tools are exposed to the LLM. Filtering is an additive YAML field we add if real-world token costs demand it.
- **No MCP tool approval flow in the UI.** GitHub PR review of the skill repo is the approval mechanism.
- **No cross-run MCP server pooling.** Each run spawns and tears down its own servers. Ephemeral runner containers make pooling impractical and unsafe.
- **No tool-call output in publish destinations.** Destinations receive the final LLM text only. Tool args/results are internal observability.
- **No CronFoundry-as-MCP-server.** This spec is client-side only.
- **No MCP resources, prompts, sampling, or roots** at MVP. Only `initialize`, `tools/list`, `tools/call`, and `notifications/cancelled`. Additive later.

## Architecture

### Per-run lifecycle

```
Runner start
  → Load SKILL.md; collect mcp_servers declarations
  → If provider is not tool-capable (currently: azure-foundry) → fail run
  → Resolve per-server env via Key Vault (from schedule.mcp_env, via manifest)
  → For each declared server, in parallel:
       Spawn subprocess (command + args + resolved env)
       MCP handshake over stdio: initialize, tools/list
       Register tools in the manager's catalog
  → Begin multi-turn loop
  → On loop end (success / bound hit / fatal error):
       SIGTERM all MCP servers; wait up to 5s; SIGKILL stragglers
       If success: proceed to memory parse → publish → writeback
       If fail: finalize with appropriate error_kind
```

### Multi-turn loop

```
messages := [ system(<env banner>), user(skill body) ]
tools    := union of servers' tools, namespaced "<server>__<tool>"
turn     := 0

loop:
    turn++
    if turn > max_turns:                fail("max_turns_exceeded"); break
    if wall_clock_exceeded(timeout_sec): fail("timeout"); break

    tr := provider.ChatTurn(ctx, messages, tools, opts)    // single turn
    messages << assistant(tr.Text, tr.ToolUses)

    if tr.ToolUses is empty:
        final_output := tr.Text
        break   // success

    results, fatal := mcp.DispatchAll(ctx, tr.ToolUses, per_tool_timeout)
    if fatal != nil:
        fail(fatal.Kind); break

    for r in results:
        messages << tool(r.ToolUseID, r.ResultJSON)
```

Key invariants:

- **Tool names are namespaced** `<server>__<tool>` so two servers exposing tools with the same name (rare but possible) stay unambiguous for dispatch.
- **Parallel tool calls within a single turn** are honored when the LLM emits multiple tool_uses in one response; the runner awaits all before composing the next turn.
- **Usage accumulates across turns**: `Usage.InputTokens` and `OutputTokens` sum; `cost_cents` is the sum. Input tokens grow with conversation length — this is the inherent cost model of tool-use loops.
- **The runner owns the loop.** Providers stay single-turn. This keeps provider adapters stateless and trivial to test.

### MCP server process model

- stdio subprocess per declared server, per run. No network exposure.
- Server stdout/stderr is captured and logged with a `mcp.<server>.*` prefix in run logs, **not** fed back into LLM messages.
- Lifetime is bounded by the runner's tool-loop function: `defer mgr.Shutdown()` guarantees cleanup on any exit path.
- Worst-case (runner container killed): Container Apps Jobs reaps the whole container; orphan sweep marks the run failed.

## Configuration Formats

### `SKILL.md` frontmatter

```yaml
---
name: weekly-digest
description: Aggregates last week's GitHub activity
model_hint: claude-opus-4-7
max_tokens: 8000
max_turns: 30                  # optional; per-skill cap, overridable per schedule
mcp_servers:
  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
  - name: fetch
    command: uvx
    args: ["mcp-server-fetch"]
---

You are writing a weekly engineering digest.

You have access to GitHub via `github__*` tools and URL fetching via `fetch__*`.
...
```

Schema rules (enforced by the existing JSON Schema at sync time):

- `mcp_servers` is a list.
- Each item has `name` (string, regex `^[a-z][a-z0-9_-]*$`), `command` (string), `args` (list of strings; optional).
- `name` is the namespace prefix used in tool names.
- `max_turns` is an integer `>= 1`; omitted = use schedule override, else global default.
- Duplicate `name`s within one skill → reject.

### `cronfoundry.yaml`

```yaml
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        provider: anthropic
        model: claude-opus-4-7
        max_turns: 40                   # optional; per-schedule override
        env:
          LOOKBACK_DAYS: "7"            # prompt banner (existing behavior)
        mcp_env:                        # new field — per-server env
          github:
            GITHUB_PERSONAL_ACCESS_TOKEN:
              secret: github_mcp_pat
          fetch: {}                     # no env needed
        destinations:
          - github-issue:
              repo: myorg/reports
              title: "Weekly digest — {{ run.date }}"
```

Schema rules:

- `mcp_env` is a map keyed by server name. Each value is a `{ENV_KEY: value}` map where value is either a string literal or `{secret: <kv_name>}` (same shape as existing `env:`).
- `max_turns` integer `>= 1` on a schedule overrides the skill-frontmatter default.
- Cross-validation at sync time:
  - Every skill-declared `mcp_servers[].name` must have an entry in the schedule's `mcp_env` (even `{}`). Missing → reject.
  - Every `mcp_env` key must refer to a declared server. Stray keys → reject.
  - Schedule uses `provider: azure-foundry` with the skill declaring `mcp_servers` → reject with a clear message.

### Global defaults

Go constants in `internal/runner`:

```go
const (
    DefaultMaxTurns            = 20
    DefaultMCPToolCallTimeoutS = 60
    MCPShutdownGracePeriod     = 5 * time.Second
)
```

Precedence for turn cap: `schedule.max_turns` → `skill.max_turns` → `DefaultMaxTurns`.

## LLM Provider Interface Changes

### New types (in `internal/llm/provider.go`)

```go
const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"   // new
    RoleTool      Role = "tool"        // new — carries a tool_result
)

type Message struct {
    Role      Role
    Content   string          // text content
    ToolUses  []ToolUse       // populated on assistant messages that called tools
    ToolUseID string          // populated on RoleTool messages; matches the tool_use
}

type ToolDef struct {
    Name        string          // namespaced: "<server>__<tool>"
    Description string
    InputSchema json.RawMessage // JSON Schema, passed straight from MCP's tools/list
}

type ToolUse struct {
    ID    string
    Name  string               // namespaced tool name
    Input json.RawMessage      // JSON, forwarded to MCP tools/call
}

type TurnResult struct {
    Text       string
    ToolUses   []ToolUse       // non-empty → runner must dispatch and re-invoke
    Usage      Usage           // per-turn; caller sums
    StopReason string          // "end_turn" | "tool_use" | "max_tokens" | ...
}
```

### New optional interface (in `internal/llm/provider.go`)

```go
type Provider interface {
    Chat(ctx, messages, opts, onChunk) (Usage, error)  // existing, unchanged
}

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

- Non-tool skills keep calling `Chat` — zero change to existing behavior, zero regression risk.
- Tool-enabled skills use `ChatTurn`. The runner type-asserts to `ToolCapableProvider` and fails the run early if the provider doesn't implement it.
- Azure Foundry keeps implementing `Provider` only until its follow-up lands.

### Per-provider translation

| Provider | Tool-def wire shape | Tool call response | Tool result next-turn |
| --- | --- | --- | --- |
| Anthropic | `tools=[{name, description, input_schema}]` | `content: [{type: "tool_use", id, name, input}]` | User message with `content: [{type: "tool_result", tool_use_id, content}]` |
| OpenAI | `tools=[{type: "function", function: {name, description, parameters}}]` | `tool_calls=[{id, function: {name, arguments}}]` on assistant message | Message `role: "tool"`, `tool_call_id`, content |

Both are straight, well-documented mappings. One translation function per provider, both tested against recorded SDK fixtures.

## New Package: `internal/mcp`

```
internal/mcp/
  manager.go       # Manager: start/shutdown/dispatch across N servers
  client.go        # single-server stdio client: init, list_tools, call_tool
  protocol.go      # MCP JSON-RPC types (request, response, notifications)
  process.go       # subprocess spawn, stdio plumbing, graceful shutdown
  redact.go        # stderr tail redaction (reuses internal/redact)
  manager_test.go
  client_test.go
```

### Manager API

```go
type Manager struct { /* ... */ }

func NewManager(ctx context.Context) *Manager

// Start launches one server. Blocks until initialize + tools/list succeed,
// the process exits, or ctx deadline hits. Registers the server with the
// Manager so it participates in Shutdown / DispatchAll.
func (m *Manager) Start(name, command string, args, env []string) error

// Tools returns the tool list reported by the named server. Read-only view.
func (m *Manager) Tools(name string) []Tool

// DispatchAll runs all tool calls in parallel against their owning servers,
// each bounded by perToolTimeout. Returns per-call results and, if any call
// hit a fatal condition (server crash, per-call timeout), a *FatalError
// describing the first one. Tool-level errors (MCP returned an error
// response) go into Results with IsError=true — the LLM gets a chance to
// adapt.
func (m *Manager) DispatchAll(
    ctx context.Context,
    calls []llm.ToolUse,
    perToolTimeout time.Duration,
) (results []CallResult, fatal *FatalError)

// Shutdown SIGTERMs each server, waits MCPShutdownGracePeriod, SIGKILLs
// stragglers. Safe to call multiple times. Idempotent.
func (m *Manager) Shutdown()

type Tool struct {
    Name        string
    Description string
    InputSchema json.RawMessage
}

type CallResult struct {
    ID         string
    ResultJSON json.RawMessage
    IsError    bool              // true if MCP returned an error response
    DurationMS int64
}

type FatalError struct {
    Kind string   // "mcp_server_crashed" | "mcp_tool_timeout"
    Err  error
}
```

### Protocol scope

MCP `2024-11-05` client surface, minimal set:

- `initialize` (no capabilities announced beyond what a bare client needs)
- `tools/list`
- `tools/call`
- `notifications/cancelled` (sent on per-call timeout)

Deliberately unimplemented at MVP: resources, prompts, sampling, roots, progress notifications. Additive later without breaking wire compatibility.

### No external MCP library dep

The protocol is a small JSON-RPC surface over stdio (~300 LOC to implement). Writing the client directly keeps supply chain narrow and removes a class of upgrade/compat surprises.

## Runner Execution Flow

Changes to `internal/runner/runner.go`. The happy path reorganizes into three phases: **setup**, **loop**, **teardown**. Non-tool skills skip setup and teardown entirely.

### Phase 1 — Setup (only if `skill.Frontmatter.MCPServers` is non-empty)

```go
toolProvider, ok := provider.(llm.ToolCapableProvider)
if !ok {
    return failWithKind(&result, "provider_tool_unsupported",
        fmt.Errorf("provider %s does not support MCP tool use", sch.Provider))
}

mgr := mcp.NewManager(ctx)
defer mgr.Shutdown()      // Phase 3, guaranteed on every exit path

for _, s := range skill.Frontmatter.MCPServers {
    env, err := resolveServerEnv(s.Name, sch.MCPEnv, in.Secrets)
    if err != nil { return failWithKind(&result, "mcp_server_start_failed", err) }
    if err := mgr.Start(s.Name, s.Command, s.Args, env); err != nil {
        return failWithKind(&result, "mcp_server_start_failed", err)
    }
}

var tools []llm.ToolDef
for _, s := range skill.Frontmatter.MCPServers {
    for _, t := range mgr.Tools(s.Name) {
        tools = append(tools, llm.ToolDef{
            Name:        s.Name + "__" + t.Name,
            Description: t.Description,
            InputSchema: t.InputSchema,
        })
    }
}
```

### Phase 2 — Loop

```go
messages := []llm.Message{
    {Role: llm.RoleSystem, Content: envBanner},
    {Role: llm.RoleUser,   Content: body},
}
maxTurns := resolveMaxTurns(sch, skill)    // schedule > skill > DefaultMaxTurns
perToolTimeout := time.Duration(DefaultMCPToolCallTimeoutS) * time.Second
totalUsage := llm.Usage{}
var finalOutput string

for turn := 1; turn <= maxTurns; turn++ {
    emitEvent("mcp.turn.start", map[string]any{"turn": turn})

    tr, err := toolProvider.ChatTurn(ctx, messages, tools, opts, onChunk)
    if err != nil { return failWithKind(&result, "llm_error", err) }
    totalUsage = totalUsage.Add(tr.Usage)

    messages = append(messages, llm.Message{
        Role: llm.RoleAssistant, Content: tr.Text, ToolUses: tr.ToolUses,
    })

    if len(tr.ToolUses) == 0 {
        finalOutput = tr.Text
        break
    }

    results, fatal := mgr.DispatchAll(ctx, tr.ToolUses, perToolTimeout)
    if fatal != nil {
        return failWithKind(&result, fatal.Kind, fatal.Err)
    }
    for _, r := range results {
        messages = append(messages, llm.Message{
            Role:      llm.RoleTool,
            ToolUseID: r.ID,
            Content:   string(r.ResultJSON),
        })
    }
}

if finalOutput == "" {
    return failWithKind(&result, "max_turns_exceeded",
        fmt.Errorf("exceeded %d turns", maxTurns))
}
```

### Phase 3 — Teardown

Scheduled via `defer mgr.Shutdown()`. Shutdown is idempotent. Logs stderr tails via `mcp.server.shutdown` events.

### Non-tool backwards-compatible path

If `skill.Frontmatter.MCPServers` is empty, the runner skips both Phase 1 and Phase 3 and calls `provider.Chat` exactly as today. Existing skills see no change.

### Resolver helpers

- `resolveServerEnv(serverName, sch.MCPEnv, secrets)`: pulls the per-server env map from `sch.MCPEnv`, resolves `{secret: <name>}` values via the existing `secrets.Resolver`. Returns `[]string` for `exec.Cmd.Env`.
- `resolveMaxTurns(sch, skill)`: returns the first non-zero of `sch.MaxTurns`, `skill.Frontmatter.MaxTurns`, `DefaultMaxTurns`.

## Data Model Changes

One small migration for two new `schedule` columns, plus additive values in existing `text`/`jsonb` columns elsewhere.

### New `schedule` columns

Migration `internal/db/migrations/20260422000002_mcp.sql`:

```sql
-- +goose Up
ALTER TABLE schedule
  ADD COLUMN mcp_env_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN max_turns    int;   -- NULL = fall back to skill frontmatter, else default

-- +goose Down
ALTER TABLE schedule
  DROP COLUMN mcp_env_json,
  DROP COLUMN max_turns;
```

- `mcp_env_json` mirrors how the existing `destinations_json` / `env_json` / `writeback_json` columns work — jsonb, synced from `cronfoundry.yaml` on each push.
- `max_turns` is a nullable integer: `NULL` → fall back to skill frontmatter, which in turn falls back to `DefaultMaxTurns`.

MCP server declarations themselves (`command`, `args`, `name`) live on the skill, not the schedule. They're part of `skill.frontmatter_json`, which already exists and is resynced on push via the existing GitHub App webhook path — no schema change there.

### No `run` schema change

`run.error_kind` and `run.status` already carry what's needed. No migration for run-side observability.

### New `run_event.event_type` values

| `event_type` | When | Payload (redacted) |
| --- | --- | --- |
| `mcp.server.start.ok` | After successful `initialize` + `tools/list` | `{server, tool_count, startup_ms}` |
| `mcp.server.start.fail` | Server exits non-zero during init, or handshake errors | `{server, error, stderr_tail}` (stderr_tail = last 1KB, redacted) |
| `mcp.server.shutdown` | Manager terminates a server | `{server, exit_code, method: "sigterm" \| "sigkill"}` |
| `mcp.server.crash` | Server exits unexpectedly mid-run | `{server, exit_code, stderr_tail}` |
| `mcp.turn.start` | Each LLM turn begins | `{turn, messages_count, tool_defs_count}` |
| `mcp.tool.call.start` | Tool dispatch starts | `{turn, server, tool, tool_use_id}` |
| `mcp.tool.call.ok` | Tool returned non-error response | `{turn, server, tool, tool_use_id, duration_ms, result_bytes}` |
| `mcp.tool.call.fail` | Tool returned error response (LLM-visible, non-fatal) | `{turn, server, tool, tool_use_id, duration_ms, error}` |
| `mcp.tool.call.timeout` | Per-tool-call timeout hit (fatal to run) | `{turn, server, tool, tool_use_id, timeout_sec}` |

Redaction discipline:

- `mcp.tool.call.*` **payloads never include tool args or tool results.** Those flow into run log files (Log Analytics), which go through the existing regex-plus-manifest redaction pipeline. Same rule already applies to LLM prompt text.
- `stderr_tail` is redacted before persisting.

### New `run.error_kind` values

`run.error_kind` is already `text`; new values are additive:

- `max_turns_exceeded`
- `mcp_server_start_failed`
- `mcp_server_crashed`
- `mcp_tool_timeout`
- `provider_tool_unsupported` (schedule declared `mcp_servers` but provider isn't tool-capable)

All of these map `run.status → failed`. They all count toward the consecutive-failure streak for auto-pause — a skill that can't converge, times out tools, or crashes servers on every run is broken, and pausing it is correct.

### Secret manifest extension

The existing run-scoped secret manifest already carries KV refs for `env:`, LLM key, and destinations. It extends to carry `mcp_env` refs under keys shaped `mcp:<server>:<ENV_NAME>`. No new manifest machinery; the runner's KV resolver picks up the new refs, and the existing "KV read logged to Log Analytics keyed by run.id" behavior covers them.

## UI

Additive. Reuses existing run-detail / live-tail / schedule-detail surfaces.

### Run detail page

- **Turn count** on the run summary card: `Turns: 4/20` next to tokens/duration/cost.
- **Timeline** renders `mcp.turn.start` and `mcp.tool.call.*` event rows inline with existing events. Each tool call row reads like:
  > `github__list_issues` · turn 2 · 180ms · ok
- **Error callouts** for new `error_kind` values (`max_turns_exceeded`, `mcp_server_start_failed`, `mcp_server_crashed`, `mcp_tool_timeout`, `provider_tool_unsupported`) use the existing error-card component — new kinds are additive string matches.

### Schedule detail page

- New **"MCP servers"** read-only section listing servers from the skill's current-SHA frontmatter. One line per server: `github — npx -y @modelcontextprotocol/server-github`.
- Next to each server, a **tool count** sourced from the most recent successful run's `mcp.server.start.ok` event. No persistent cache needed; live-read on page load.

### Skills view

- Skills with non-empty `mcp_servers` get a small **tool icon** in the skill list. No separate tools tab.

### Deliberately absent

- No YAML editor for MCP config.
- No per-run tool arg/result inspector. Args/results live in Log Analytics and are reachable via the existing live-tail/log-tail views.
- No tool-approval flow — PR review of `SKILL.md` and `cronfoundry.yaml` is the approval mechanism.
- No "try a tool" affordance. Manual "Run now" is how tools get exercised.

## Testing

### Unit — `internal/mcp` (new package)

A stub MCP server binary under `testdata/mcp-fixtures/` is the workhorse for all MCP tests:

- **Protocol round-trip**: stub responds to `initialize`, `tools/list`, `tools/call` with multi-content-block results. Client parses correctly.
- **Server crash mid-call**: stub exits during a call → `DispatchAll` returns `FatalError{Kind: "mcp_server_crashed"}` with exit code captured.
- **Per-call timeout**: stub sleeps 5s; `DispatchAll(..., perToolTimeout: 100ms)` returns `FatalError{Kind: "mcp_tool_timeout"}`.
- **Parallel dispatch**: stub logs call-receive timestamps; concurrent calls land within a few ms of each other.
- **Tool-level error (non-fatal)**: stub returns an MCP error response → `CallResult.IsError=true`, `FatalError` nil.
- **Graceful shutdown**: `Shutdown()` sends SIGTERM; SIGKILL fallback after `MCPShutdownGracePeriod`.
- **Manager isolation**: two servers with an identical inner tool name; namespacing (`<server>__<tool>`) keeps dispatch unambiguous.

### Unit — `internal/runner` (extended)

- **Non-tool path unchanged**: skill without `mcp_servers` still calls `provider.Chat`; existing test suite passes.
- **Tool path happy**: fake `ToolCapableProvider` emits one `tool_use`, then `end_turn`. Fake Manager returns a canned result. Assert: messages composed correctly, `totalUsage` summed, final text published.
- **Max turns exceeded**: fake provider emits `tool_use` every turn. Run fails with `error_kind="max_turns_exceeded"` after `max_turns` iterations.
- **Provider-without-tools rejected**: schedule declares mcp_servers, provider is Azure Foundry; run fails fast with `error_kind="provider_tool_unsupported"`.
- **Env resolution**: `mcp_env` has a `{secret: ...}` entry; resolver is called; env reaches the subprocess (assert via fake Manager's recorded env).
- **Multi-turn usage sum**: 3 turns with distinct usage counts; `result.Usage` is the sum.
- **Parallel tool calls in one turn**: provider emits 2 tool_uses; runner dispatches both; both tool_result messages appear in the next turn's messages in a deterministic order.

### Unit — `internal/llm/{anthropic,openai}`

- **Tool-def translation**: `ToolDef → provider-native tool spec`. Round-trip against recorded SDK fixture.
- **Tool-use parse**: recorded provider response with `tool_use`/`tool_calls` → `TurnResult.ToolUses` shape.
- **Multi-tool in one turn**: recorded response with two tool calls → two `ToolUses`.
- **Non-tool response (text-only)**: existing `Chat` behavior unaffected.

### Unit — `internal/config/{manifest,skill}` (extended)

- **Skill frontmatter parse**: `mcp_servers` with 0, 1, N items; invalid `name` chars rejected; missing `command` rejected; `max_turns` parses.
- **Manifest schedule parse**: `mcp_env` keyed by server name; secret refs resolve; empty map `{}` valid.
- **Cross-validation at sync time**:
  - Skill declares `mcp_servers`, schedule omits `mcp_env` for one → reject.
  - Schedule's `mcp_env` references an undeclared server → reject.
  - `provider: azure-foundry` + non-empty `mcp_servers` → reject with a clear message.

### Integration — e2e with stub server

- Build the stub fixture into a small Go binary under `testdata/`.
- Drive scheduler → runner → finalize with a fake LLM that issues two tool calls then ends. Stub MCP server handles them.
- Assert: run row `status=succeeded`, token counts positive, `run_event` contains ordered `mcp.turn.start`, `mcp.tool.call.ok` × 2, `mcp.server.shutdown`.
- Assert: no lingering subprocesses post-finalize (scan `/proc` in the test container).

### Negative — one test per failure kind

- `mcp_server_start_failed` — fixture exits 1 during `initialize`.
- `mcp_server_crashed` — fixture self-kills mid-call.
- `mcp_tool_timeout` — fixture sleeps past `perToolTimeout`.
- `max_turns_exceeded` — fake provider loops forever.
- `provider_tool_unsupported` — schedule wired to Azure Foundry.

Each produces the matching `error_kind`, writes a `run_event`, and (paired with spec #3) counts toward the auto-pause streak.

### Build / deps

- Runner image adds Node 20 + Python 3.12 + `uvx` for the most common MCP server launchers. ~300MB image size increase, ~2s cold-start add on Container Apps Jobs.
- CI: `go test ./internal/mcp/...` and `./internal/runner/...` gain a fixture-binary build step.

## Rollout

Single PR covers: migration for the two new `schedule` columns, new `internal/mcp` package, provider-interface extension, runner refactor, config schema extension, UI additions, runner image dep bump, tests.

- One small additive migration (`mcp_env_json`, `max_turns`). Defaults backfill cleanly; no data rewrites.
- Backwards compatible: skills without `mcp_servers` behave identically.
- Purely opt-in at the skill level; no feature flag.
- The API, scheduler, and runner images all ship together (migration is embedded in the API binary and applied on its startup, as with other migrations).

On deploy:

1. Push the new API, scheduler, and runner images.
2. Update Container Apps / Jobs image tags.
3. API startup applies the migration; existing schedules continue running unchanged (defaults on both new columns).
4. Operators can add `mcp_servers:` to any skill and `mcp_env:` to the paired schedule to opt in.

## Deferred / Future

Explicitly out of scope; revisit once this ships:

1. **HTTP/SSE MCP transport** — for hosted MCP services.
2. **Azure AI Foundry provider support** — once per-deployment-model tool capability detection lands.
3. **Per-tool allow/deny filtering** on each server, to cap input-token context on servers with large tool surfaces.
4. **MCP resources and prompts** surfaces.
5. **Sampling** (`sampling/createMessage`) — server-initiated LLM calls.
6. **Cross-run server pooling** if cold starts become a bottleneck in practice.
7. **Tool-arg/result inspector** in the UI — a dedicated component for drilling into a specific tool call.
8. **Per-run MCP config in `cronfoundry.yaml`** — allow a schedule to enable only a subset of the skill's servers.
