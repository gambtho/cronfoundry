# Release-Readiness Session — Context Handoff

**Date:** 2026-04-29
**Session source:** Subagent-driven release-readiness review of cronfoundry, conducted via the `superpowers:` skill set.

This document is a self-contained briefing for picking up the remaining release-readiness work in a fresh session. Read this first; then pick a prompt from `prompt-item-<n>.md`.

---

## What this project is

CronFoundry is a self-hostable Azure-deployable scheduler for LLM "skills." A
GitHub repo holds skill prompts + a `cronfoundry.yaml` manifest of schedules;
the scheduler fires them on cron, runs the LLM call, and publishes output to
Slack / Discord / Teams / GitHub-issue / HTTP / SMTP, with optional
write-back of `<memory>...</memory>` blocks to the repo. Single-tenant,
single-replica today. Go 1.25 + React + Postgres + Azure Container Apps Jobs
(plus AKS / Fly.io adapters). MVP is shipped per the README; the design and
PRD docs are at `docs/superpowers/specs/2026-04-19-cronfoundry-{design,prd}.md`.

## What we did this session

A `/improve`-style holistic review surfaced **10 release-readiness items** —
gaps that block an actual public release of the project. Six shipped as PRs
in this session; four remain.

### Shipped (6 PRs)

| # | Item | PR | Branch | Worktree |
|---|---|---|---|---|
| 1 | Add MIT LICENSE | [#35](https://github.com/gambtho/cronfoundry/pull/35) | `chore/add-mit-license` | `.worktrees/add-license` |
| 2 | CSRF middleware (double-submit cookie + Origin check) | [#38](https://github.com/gambtho/cronfoundry/pull/38) | `worktree-spec-csrf` | `.claude/worktrees/spec-csrf` |
| 3 | Per-IP rate limiting + SSE concurrency cap | [#39](https://github.com/gambtho/cronfoundry/pull/39) | `worktree-spec-ratelimit` | `.claude/worktrees/spec-ratelimit` |
| 4 | Prometheus `/metrics` endpoint | [#40](https://github.com/gambtho/cronfoundry/pull/40) | `worktree-spec-metrics` | `.claude/worktrees/spec-metrics` |
| 8 | `make worktree-clean` target | [#36](https://github.com/gambtho/cronfoundry/pull/36) | `chore/worktree-cleanup` | `.worktrees/worktree-cleanup` |
| 9 | Spec status headers + index | [#37](https://github.com/gambtho/cronfoundry/pull/37) | `docs/spec-status-index` | `.worktrees/spec-index` |

All six branches were off `main` and don't depend on each other; they should
merge cleanly modulo a small `cmd/cronfoundry/serve.go` overlap between
items #2, #3, and #4 (each adds env-var reads in the same Deps construction
block).

### Remaining (4 items)

| # | Item | Type | Brainstorm? |
|---|---|---|---|
| 5 | `queue` overlap policy is documented but unimplemented | Correctness | Yes — implement vs. reject |
| 6 | Runbook simplification | Docs / UX | Yes — what to cut/automate |
| 7 | Secret-resolver split-brain (`secrets/` vs `secretstore/`) | Architecture | Yes — target shape |
| 10 | E2E test in CI | Tests / CI | No — straightforward |

Each has a dedicated prompt file: `prompt-item-5.md`, `prompt-item-6.md`,
`prompt-item-7.md`, `prompt-item-10.md`.

## How we worked (process)

The flow we used per item:

1. **Brainstorm** (`superpowers:brainstorming` skill) — clarifying questions
   one at a time via `AskUserQuestion`, then 2-3 approaches with a
   recommendation, settle on a direction.
2. **Spec** — write to `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`.
   Format: `**Status:** Proposed`, problem statement, mechanism, components,
   tests, operational notes, out-of-scope, acceptance criteria.
3. **Plan** (`superpowers:writing-plans` skill) — write to
   `docs/superpowers/plans/YYYY-MM-DD-<topic>.md` with bite-sized TDD tasks
   (failing test → minimal implementation → run → commit), exact code in
   each step, no placeholders.
4. **Execute** (`superpowers:subagent-driven-development`) — fresh
   general-purpose subagent per task, two-stage review (spec compliance + code
   quality) for high-risk tasks; mechanical tasks shipped without review.
5. **PR** — push, `gh pr create` with a HEREDOC body listing summary, operator
   notes, and a checked test plan.

Everything happens in a git worktree under `.claude/worktrees/spec-<topic>` —
**never edit the primary checkout** (`/home/tng/workspace/cronfoundry`
itself), and **never push to `main`**. Both rules are recorded in
`~/.claude/projects/-home-tng-workspace-cronfoundry/memory/`.

## Project conventions worth knowing

- **Go 1.25**, single-module repo, `go.mod` at root.
- Hexagonal-ish layout: `internal/domain` doesn't exist as a top-level
  package — the architecture is `cmd/{cronfoundry,runner}` + `internal/<feature>`
  packages. Each `internal/<feature>` typically contains its types, the HTTP
  handler if relevant, and a `_test.go`.
- HTTP handlers: stdlib `net/http` + `mux.Handle("METHOD /path", handler)` —
  no framework. Middleware is `func(http.Handler) http.Handler`.
- DB: `pgx/v5` + `sqlc`-generated code in `internal/db/gen/`.
  Migrations: `goose` in `internal/db/migrations/`.
- React UI: `web/`, Vite + React 18 + react-query + Tailwind. Built into
  `internal/webapi/web/dist/` and embedded via `embed.FS`.
- Tests: `stretchr/testify`. Postgres-dependent tests use `testdb.BootPG(t)`
  (testcontainers); they `t.Skip` under `-short`. Most webapi handler tests
  go through `mux.ServeHTTP`, which means they pass through middleware
  (CSRF, rate limit, etc.) — be mindful when adding new tests.
- Linting: CI runs `go vet` + golangci-lint via the action with default
  config (no `.golangci.yml` in the repo).
- IDE warnings about gopls "module not in workspace" are stable noise when
  working in `.claude/worktrees/*` — real `go test` and `go vet` are clean.
  Trust the CLI, not gopls, when working in a worktree.

## How to start a new item

```bash
cd /home/tng/workspace/cronfoundry
git worktree add .claude/worktrees/spec-<topic> -b worktree-spec-<topic> main
# Open Claude Code or another agentic session inside that worktree.
# Then: paste the contents of `prompt-item-<n>.md` into the agent.
```

The agent should follow the same brainstorm → spec → plan → execute → PR
loop. Skip brainstorming only for items with "Brainstorm? No" in the table
above (item #10).

## Memory / preferences

The user's `~/.claude/projects/-home-tng-workspace-cronfoundry/memory/` has:

- `feedback_pr_workflow.md` — never push to main; always use the open PR branch
- `feedback_worktrees.md` — every edit happens in a worktree, not the primary
- `feedback_env_secrets.md` — `.env` at repo root holds API keys, GitHub App creds, PEM path

A new agent will pick those up automatically; restating them is fine but not
required.

## State of `cmd/cronfoundry/serve.go` (merge note)

Three of the open PRs (#38, #39, #40) each add env-var reads to the same
block in `cmd/cronfoundry/serve.go` near the Deps construction. The merge
order doesn't matter — each PR's diff is small and the conflicts are
mechanical. The deferred `slog.Warn` for "PUBLIC_BASE_URL is set but
TRUST_PROXY is not" should be added during the second-merging-PR's resolution.

## State of CI

`.github/workflows/ci.yml` runs `go vet`, full `go test ./...` (with a
service Postgres), and golangci-lint via action. There is **no** end-to-end
or smoke-test job, despite a `make e2e` target existing. Closing that gap is
item #10.

`.github/workflows/release.yml` builds and pushes container images on `v*`
tag pushes.

## Where to look

- Specs: `docs/superpowers/specs/` (all 19 prior + 4 added this session)
- Plans: `docs/superpowers/plans/` (TDD task breakdowns)
- Spec index: `docs/superpowers/specs/README.md` (added in PR #37)
- Architecture overview: README.md "Architecture" block
- Overall design: `docs/superpowers/specs/2026-04-19-cronfoundry-design.md`
- Product requirements: `docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`
- Operator runbook: `docs/guides/smoke-test-mvp-azure.md`
  (24 findings logged in `smoke-test-mvp-azure-findings.md` — see item #6)
