# MCP Tool Support — Phase 5: UI + Runner Image + Final Gate

## Context

Phases 1–4 of MCP tool support have been merged (PR #18 on `spec/mcp-tools`). The backend is complete:

- `internal/mcp`: JSON-RPC 2.0 stdio client with process lifecycle, tool listing, parallel dispatch
- `internal/llm`: `ToolCapableProvider` with `ChatTurn` for Anthropic and OpenAI
- `internal/runner`: Dual-path execution — single-shot `Chat` for non-tool skills, multi-turn `ChatTurn` loop for tool-aware skills
- `internal/api`: `RunContext` response surfaces `mcp_env`, `max_turns`, and includes MCP env secrets in the manifest
- `cmd/cronfoundry/runner.go`: Production runner forwards MCPServers/MCPEnv/MaxTurns from API to `runner.RunInput`
- DB: `mcp_env_json` and `max_turns` columns on `schedule`, exposed via `GetRunForContext`
- E2E test: `TestE2E_MCPToolLoop` validates the full stub-server → Anthropic tool_use → dispatch → end_turn pipeline

Phase 5 has three tasks remaining. Work on a new branch off `main` (after PR #18 merges).

## Task 1: UI — Surface MCP servers and turn count

### Backend: schedule list endpoint

Extend `internal/webapi/schedules.go` list handler to include `mcp_servers` (parsed from the skill's `frontmatter_json` via the existing JOIN against `skill`) and `max_turns` in the schedule list response. The frontend needs these fields to show which schedules use tools.

### Frontend types (`web/src/lib/types.ts`)

Add to the existing `Schedule` interface:

```typescript
max_turns: number | null
mcp_servers: MCPServerRef[]
```

Add a new interface:

```typescript
export interface MCPServerRef {
  name: string
  command: string
  args: string[]
}
```

### Dashboard (`web/src/pages/Dashboard.tsx`)

On schedule cards where `mcp_servers.length > 0`, add a small badge showing the tool count with a tooltip listing server names. Use existing Tailwind utility classes (e.g., `bg-indigo-900 text-indigo-200`).

### Runs page (`web/src/pages/Runs.tsx`)

The run-events timeline already renders `event_type` + `payload_json`. The runner emits MCP-specific event types (`mcp.turn.start`, `mcp.tool.call.ok`, `mcp.tool.call.fail`, `mcp.tool.call.timeout`, `mcp.server.start.ok`). Add a prettifier function that formats these into human-readable strings:

- `mcp.turn.start` → "Turn N"
- `mcp.tool.call.ok` → "server__tool · Nms · ok"
- `mcp.tool.call.fail` → "server__tool · error"
- `mcp.tool.call.timeout` → "server__tool · timeout"
- `mcp.server.start.ok` → "MCP server X ready (N tools)"

Integrate this into the existing event-type rendering logic.

**Important**: If the runner's tool loop does not yet emit `mcp.*` run_event types (they may not have been added in Phase 4), backfill them in the runner: emit events before/after each `mgr.DispatchAll` dispatch and when servers start. Then re-run runner tests.

### Verify

```bash
cd web && npm run build
```

## Task 2: Runner Dockerfile with Node + Python + uvx

MCP servers are arbitrary stdio subprocesses — the most common ones are npm packages or Python tools run via `uvx`. The runner image needs these runtimes.

### Create `deploy/Dockerfile.runner`

- Build stage: `golang:1.25-alpine`, build `./cmd/runner`
- Runtime stage: `debian:12-slim` with `ca-certificates`, `curl`, `nodejs`, `npm`, `python3`, `python3-pip`, `git`
- Install `uv` via pip (`pip3 install --break-system-packages --no-cache-dir uv`) — this provides `uvx`
- Copy the runner binary, create a non-root `runner` user, set as entrypoint

### Shrink `deploy/Dockerfile`

The main API+scheduler image should stay distroless. Remove the runner binary from it if present — only build `./cmd/cronfoundry`.

### Verify

```bash
docker build -f deploy/Dockerfile -t cronfoundry-api-test .
docker build -f deploy/Dockerfile.runner -t cronfoundry-runner-test .
```

### Update CI

If `.github/workflows/*.yml` references a single Dockerfile, add a second build step for the runner image. Update any operator guides (e.g., `docs/guides/smoke-test-mvp-azure.md`) to mention the separate runner image.

## Task 3: Final gate — lint, vet, full tests, web build, manual smoke

Run the full quality gate:

```bash
go vet ./...
make lint          # if available
go test ./... -count=1 -timeout 15m
cd web && npm run build
```

### Manual smoke (strongly recommended)

1. Build the runner image locally and deploy to a scratch CronFoundry instance
2. Configure a test skill with `mcp_servers: [{name: fetch, command: uvx, args: [mcp-server-fetch]}]`
3. Set up the schedule with Anthropic credentials and trigger manually
4. Confirm: run completes in 1–2 turns, LLM output reaches destinations, no lingering `uvx` processes in the container

## Known constraints

- No Azure AI Foundry support for MCP (cross-validation rejects the combination at sync time)
- No HTTP/SSE MCP transport — all servers are stdio subprocesses
- Runner image is ~300MB (vs distroless ~20MB) — adds ~2s cold start on Container Apps Jobs
- No observability for MCP tool arg/result bodies in the UI (they flow to Log Analytics via the existing redaction pipeline)

## Self-review checklist

- [ ] Dashboard shows a tool icon on schedules with MCP servers
- [ ] Runs page renders MCP tool-call events in the timeline
- [ ] `npm run build` passes
- [ ] Runner Dockerfile builds and ships Node, Python, uvx, git
- [ ] API+scheduler Dockerfile stays distroless (no runtime deps)
- [ ] CI builds both images
- [ ] `go vet ./...` clean
- [ ] `go test ./... -count=1 -timeout 15m` passes
- [ ] All new `run.error_kind` values are covered by a test case
