# CronFoundry P2 — Service Layer Design

**Status:** Shipped (3b246ff)
**Date:** 2026-04-19
**Companion documents:**
- [P1 design](./2026-04-19-cronfoundry-design.md) (master architecture)
- [PRD](./2026-04-19-cronfoundry-prd.md) (product requirements)
- [P1 plan](../plans/2026-04-19-p1-core-runner.md) (shipped as tag `v0.1.0-p1`)

## Overview

P2 adds the always-on service layer on top of the P1 runner CLI: a Postgres
database, a repo-sync poller, a cron-based scheduler, an `/internal` HTTP API
that the runner speaks, and an envelope-encrypted secret store. The service
runs locally today (single binary + docker-compose) and is architected so P4
can swap in Azure Container Apps Jobs and Azure Key Vault behind the same
interfaces without changes to application code.

P1 shipped a subprocess (`cronfoundry-runner`) that can execute one skill
invocation end-to-end. P2 promotes that subprocess to a managed unit: schedules
fire it on cron, the runner authenticates to an API for its per-run context,
and results stream back to a Postgres-backed run history.

P2 explicitly defers the web UI (→ P3), Azure deployment (→ P4), GitHub
webhook ingestion (→ P2.5 or P3), and all fast-follow features already listed
in the master design.

## Goals

- A self-hoster can run CronFoundry on their laptop with `docker-compose up`
  and schedule real skills against real GitHub repos.
- The runner is a subordinate unit with zero direct DB / Key-Vault
  credentials; everything it needs arrives through a per-run bearer token.
- Secret values never transit the DB as plaintext and never appear in logs.
- Cloud-specific concerns (job dispatch, secret backing store, identity) live
  behind small interfaces so P4 can substitute Azure implementations without
  touching the scheduler, API, or runner.
- Run history, schedule state, and connected repos survive service restarts.
- Scheduled fires are idempotent under duplicate ticks and partition-safe if
  the service ever runs multiple instances.

## Non-Goals

- Web UI. The surface exposed in P2 is `/internal/*` (runner-facing) plus a
  `cronfoundry admin` CLI. Browsers are a P3 concern.
- Human authentication. Users, roles, sessions, and allowlists come in P3.
- GitHub webhook ingestion. Polling-based sync is the sole P2 mechanism.
  Webhook support lands when there is a public hostname to receive on.
- Azure-specific features — Container Apps Jobs dispatcher, Key Vault secret
  store, Bicep templates, GHCR publishing. All are P4.
- Multi-tenant quotas, billing, sign-up — schema is tenant-aware from day one
  (single seeded organization in MVP), but enforcement is post-MVP.
- Live-tail log streaming to a UI. Run events are persisted structured rows
  visible via the API and CLI; websocket fan-out is P3.

## High-Level Architecture

### Process topology

P2 runs as a single binary `cronfoundry` with three subcommands:

- `cronfoundry serve` — API + scheduler + sync poller in one process (three
  goroutines). Binds `127.0.0.1:8080` by default.
- `cronfoundry runner` — executes one run. Dual-mode:
  - **HTTP mode** (P2 production path) — reads `CRONFOUNDRY_RUN_ID`,
    `CRONFOUNDRY_API_URL`, `CRONFOUNDRY_RUN_TOKEN` from env, fetches run
    context from the API, streams events back.
  - **Standalone CLI mode** (P1 compat, dev smoke tests) — same flag surface
    as `cronfoundry-runner` in v0.1.0-p1.
- `cronfoundry admin` — one-shot operator utilities (init, connect-repo,
  set-secret, list-schedules, trigger, rotate-master-key).

A single binary keeps deployment simple. Scheduler/API separation into two
binaries is a P4+ refactor if Azure Container Apps Environment demands it; the
current packaging preserves that option (each loop is a package-level
`func Run(ctx, *Deps) error`).

### Subsystems

| Package | Purpose |
| --- | --- |
| `internal/db/` | pgx pool, sqlc-generated queries, goose migrations embedded in the binary |
| `internal/secretstore/` | Envelope-encrypted secret read/write; interface-backed so P4 swaps in Key Vault |
| `internal/github/` | App JWT, installation-token cache, clone helpers using install tokens |
| `internal/sync/` | Loop 1: polls connected repos, re-parses `cronfoundry.yaml` + `SKILL.md`, upserts schedule rows |
| `internal/scheduler/` | Loop 2+3: cron tick, dispatch, runner-process supervision, orphan sweep |
| `internal/api/` | HTTP handlers for `/internal/*` (run context, secrets, events, finalize, manual trigger) |
| `internal/token/` | Per-run JWT sign / verify / introspect |
| `internal/cloud/` | Dispatcher + secret-store interfaces with localhost implementations |
| `internal/serve/` | Wires the three loops together, graceful shutdown orchestration |
| `cmd/cronfoundry/` | `serve.go`, `runner.go`, `admin.go` — entry points |

All P1 packages (`internal/config`, `internal/llm`, `internal/publish`,
`internal/writeback`, `internal/runner`, etc.) stay in place and are
library-imported by both the HTTP-mode runner and the standalone runner.

### End-to-end data flow for a scheduled fire

```
sync poller:
  tick (per-connection interval) → GitHub HEAD check → conditional clone
  → config.ParseManifest/ParseSkillFile → upsert skill + schedule rows

scheduler tick (30s):
  SELECT schedules WHERE enabled AND next_fire_at <= now
  → INSERT run (schedule_id, fire_time, status=pending)  -- UNIQUE (schedule_id, fire_time)
  → UPDATE schedule SET next_fire_at = <next cron time>
  → dispatch(run)

dispatch:
  token := sign_jwt({run_id, secret_refs, exp})
  runner_token_hash := sha256(token); store on run row
  exec.CommandContext(ctx, self, "runner", "--run-id", run.id)
    env: CRONFOUNDRY_API_URL, CRONFOUNDRY_RUN_ID, CRONFOUNDRY_RUN_TOKEN
  supervisor goroutine: cmd.Wait(); mark failed/crash if non-terminal

runner process:
  GET /internal/runs/{id}/context      -- skill coords, destinations, env refs, writeback cfg
  GET /internal/secrets?names=...      -- cleartext, scoped by JWT secret_refs claim
  GET /internal/repos/{id}/clone-url   -- short-lived HTTPS URL with install token
  [reuse P1 runner package: clone → LLM stream → memory parse → publish → writeback]
  POST /internal/runs/{id}/events      -- batched every 2s or 10 events
  POST /internal/runs/{id}/finalize    -- status, duration, tokens, cost, publish_results
```

### Azure portability boundary

Two interfaces, localhost impls in P2, Azure impls in P4:

```go
// internal/cloud/dispatcher.go
type Dispatcher interface {
    Dispatch(ctx context.Context, spec DispatchSpec) (Handle, error)
}
// P2 impl: SubprocessDispatcher (exec.CommandContext)
// P4 impl: ContainerAppsJobDispatcher (ARM API)

// internal/cloud/secretstore.go
type SecretStore interface {
    Get(ctx context.Context, name string) (string, error)
    Put(ctx context.Context, name, value string) error
    Delete(ctx context.Context, name string) error
}
// P2 impl: EnvelopePostgresStore (master key + per-secret DEK)
// P4 impl: KeyVaultStore (managed identity + KV SDK)
```

Application code (scheduler, API) depends on the interfaces, not the impls.

## Data Model

All tables carry `organization_id uuid` from day one. A single `organization`
row is seeded by `cronfoundry admin init` on first startup.

```sql
organization (id, name, created_at)

repo_connection (
  id, org_id, github_app_install_id, owner, name, default_branch,
  sync_interval_sec DEFAULT 60,
  last_synced_at, last_synced_head_sha, last_sync_error,
  created_at,
  UNIQUE (org_id, owner, name)
)

skill (
  id, org_id, repo_id FK→repo_connection CASCADE,
  path, name, current_sha, frontmatter_json JSONB, updated_at,
  UNIQUE (repo_id, path)
)

schedule (
  id, org_id, skill_id FK→skill CASCADE,
  name, cron, timezone DEFAULT 'UTC',
  overlap_policy DEFAULT 'skip',
  timeout_sec DEFAULT 600,
  enabled BOOLEAN DEFAULT true,
  provider, model,
  llm_secret_ref, llm_endpoint, llm_deployment,
  destinations_json JSONB NOT NULL,
  writeback_json JSONB,
  env_json JSONB DEFAULT '{}',
  next_fire_at,
  created_at, updated_at,
  UNIQUE (skill_id, name)
)

run (
  id, org_id, schedule_id FK→schedule CASCADE,
  skill_sha,
  fire_time,              -- the schedule's next_fire_at at the moment of INSERT; null for manual fires
  status,                 -- pending|running|succeeded|partial_failure|failed
  fire_reason,            -- schedule|manual
  actor,                  -- github login for manual, null for schedule
  started_at, finished_at, duration_ms,
  tokens_in, tokens_out, cost_cents,  -- int cents
  error_kind,             -- clone|parse|llm|publish|writeback|timeout|crash|shutdown
  error_msg,
  writeback_commit_sha,
  runner_pid,
  runner_token_hash,      -- sha256 of the per-run bearer JWT
  created_at,
  UNIQUE (schedule_id, fire_time)    -- idempotent ticks; null-safe: manual fires never collide
)
-- INDEX: (schedule_id, created_at DESC)
-- INDEX: (status, created_at) WHERE status IN ('pending','running')

run_event (
  id BIGSERIAL, run_id FK→run CASCADE,
  ts, level,             -- info|warn|error
  event_type,            -- llm.start|publish.slack.ok|writeback.commit.ok|...
  payload_json JSONB DEFAULT '{}'
)
-- INDEX: (run_id, ts)

secret (
  id, org_id, name,
  dek_wrapped BYTEA,     -- AES-256-GCM(MASTER_KEY, DEK)
  ciphertext BYTEA,
  nonce BYTEA,
  version INT DEFAULT 1,
  created_at, updated_at, last_used_at,
  UNIQUE (org_id, name)
)

audit_log (
  id BIGSERIAL, org_id, actor,
  action, target_kind, target_id,
  ts, detail_json JSONB DEFAULT '{}'
)
-- INDEX: (org_id, ts DESC)
```

### Design notes on the schema

- `run_event.id` is `BIGSERIAL` for tight packed primary keys and ordered
  inserts; millions of events fit comfortably.
- `run.cost_cents` is `INT` (whole cents). ~$21 M per-run cap is fine for LLM
  skills; switch to `BIGINT` later if we're ever wrong.
- `run_event.payload_json` rejects payloads containing any currently-active
  secret substring at insertion time (runtime check in `internal/api`).
- `schedule.destinations_json` and `writeback_json` store the parsed YAML
  subtree verbatim. Renormalizing into relational tables buys nothing —
  destinations are write-rarely, read-per-run.
- No `user` table in P2. All `/internal` endpoints authenticate with the
  per-run bearer token. Human access is CLI (operator shell) only.
- No `github_app_installation` table. `github_app_install_id` lives directly
  on `repo_connection`. Multi-account support is deferred.

## Control Flow

Three concurrent loops inside `cronfoundry serve`, each a `func Run(ctx, *Deps) error`.

### Loop 1 — Repo sync poller

Tick cadence per repo: `repo_connection.sync_interval_sec` (default 60s).

```
for each repo where last_synced_at + interval <= now:
  token := github.InstallationToken(install_id)    -- cached ~50 min TTL
  head_sha := github.GetBranchHead(owner, name, default_branch)
  if head_sha == last_synced_head_sha:
    mark last_synced_at = now; continue
  clone --depth=1 --branch=<default_branch>   -- authenticated with install token
  parse cronfoundry.yaml
  for each skill entry:
    read SKILL.md, resolve {{ include }} (P1 config package)
  validate(manifest)
  begin tx:
    upsert skill rows (keyed on repo_id + path)
    for each schedule entry:
      upsert schedule row; recompute next_fire_at from cron+tz
    mark disappeared schedules as enabled=false (soft-disable, preserves run history)
  commit
  mark last_synced_at = now; last_synced_head_sha = head_sha; clear last_sync_error
  remove temp clone directory
on error: write last_sync_error; don't block other repos
```

- HEAD-first cheap-check: clones only when SHA has changed.
- Per-repo rate-limit is the sync interval; no global limit needed at MVP scale.
- Schedule deletion semantics: soft-disable on disappearance, not hard-delete.
  Run history stays queryable. A future cleanup pass can hard-delete after a
  grace window.

### Loop 2 — Scheduler tick

Cadence: 30s global ticker.

```
query schedules where enabled AND next_fire_at <= now ORDER BY next_fire_at
for each due schedule:
  begin tx:
    INSERT run (schedule_id, fire_time=next_fire_at, status=pending)
      ON CONFLICT (schedule_id, fire_time) DO NOTHING
    UPDATE schedule SET next_fire_at = <next cron time via robfig/cron/v3>
  commit
  if insert actually happened:
    apply overlap policy (see below)
    dispatch(run) unless skipped
orphan sweep:
  UPDATE run SET status='failed', error_kind='shutdown'
    WHERE status IN ('pending','running') AND now - coalesce(started_at, created_at)
          > schedule.timeout_sec + 300
```

Overlap policies (evaluated at dispatch):
- `skip` (default): if the schedule has any non-terminal run, delete this
  pending row and do not dispatch
- `queue`: leave pending; a subsequent tick dispatches once prior finishes
- `concurrent`: dispatch regardless

`next_fire_at` computation uses `robfig/cron/v3` parser
(`cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)`)
with each schedule's configured timezone.

### Loop 3 — Dispatch + supervision

```
dispatch(run):
  token := token.Sign({run_id, org_id, secret_refs, exp=now+timeout+120s})
  update run.runner_token_hash = sha256(token)
  env := {
    CRONFOUNDRY_API_URL: <api base, typically http://127.0.0.1:8080/internal>,
    CRONFOUNDRY_RUN_ID:  run.id,
    CRONFOUNDRY_RUN_TOKEN: token,
  }
  cmd := exec.CommandContext(ctx, os.Executable(), "runner", "--run-id", run.id)
  cmd.Env = append(os.Environ(), env...)
  cmd.Stdout, cmd.Stderr = structuredLogCapture(run.id)   -- feeds into redactor
  cmd.Start()
  run.runner_pid = cmd.Process.Pid
  go supervise(cmd, run.id):
    err := cmd.Wait()
    if err != nil and run.status still non-terminal:
      mark failed, error_kind=crash, error_msg=err
```

On SIGTERM to `cronfoundry serve`:
1. Stop accepting new schedule ticks.
2. `SIGTERM` to every live runner PID.
3. Wait 30s; `SIGKILL` any still running.
4. Orphan-sweep non-terminal runs to `failed`, `error_kind=shutdown`.

### Runner → API flow

```
GET  /internal/runs/{id}/context   → RunContext {
  skill: { repo_id, sha, path, frontmatter_json, body },
  schedule: { provider, model, timeout_sec, env, destinations_json, writeback_json },
  llm: { provider, model, llm_secret_ref, endpoint?, deployment? },
  secret_refs: [slack_webhook, openai_key, ...]  -- for validation only
}
GET  /internal/secrets?names=a,b,c     → { a: "val", b: "val", c: "val" }
  -- decrypted server-side; scoped to JWT's secret_refs claim
GET  /internal/repos/{id}/clone-url    → { url: "https://x-access-token:<tok>@github.com/..." }
POST /internal/runs/{id}/events        → 204
  body: { events: [{type, ts, payload}, ...] }
POST /internal/runs/{id}/finalize      → 204
  body: { status, started_at, finished_at, tokens_in, tokens_out,
          cost_cents, publish_results, writeback_sha?, error_kind?, error_msg? }
```

### Failure matrix

| Failure | run.status | error_kind | Side effect |
| --- | --- | --- | --- |
| Poll clone fails but cron still fires | `failed` | `clone` | No publishes, no writeback |
| Skill / manifest parse fails at run time | `failed` | `parse` | Same |
| LLM retries exhausted | `failed` | `llm` | Same |
| Output OK, all destinations OK, writeback OK | `succeeded` | — | — |
| One destination 4xx/5xx after retries | `partial_failure` | — | Others publish, writeback proceeds |
| Writeback commit or push fails | `partial_failure` | — | Destinations publish, warning event |
| Runner OOM / panic | `failed` | `crash` | Supervisor goroutine catches |
| Wall-clock timeout hit | `failed` | `timeout` | Context cancellation; sweeper backup |
| `cronfoundry serve` restart mid-run | `failed` | `shutdown` | Startup orphan sweep |

## Security Model

### Principals

| Principal | Credentials | Scope |
| --- | --- | --- |
| `cronfoundry serve` | `DATABASE_URL`, `MASTER_KEY`, `GITHUB_APP_PEM` from env | Full DB RW, encrypts/decrypts secrets, signs App JWTs, signs per-run JWTs |
| Runner subprocess (per run) | Per-run bearer JWT via env | Only `/internal/runs/{id}/...` for its specific run ID, plus `GET /internal/secrets?names=...` filtered to `secret_refs` JWT claim |
| GitHub App | App private key (held by `serve`) | Per-install perms chosen by repo owner: `contents:read+write`, `issues:write`, `metadata:read` |
| Self-host operator | SSH / shell access | Everything — owns the host |

### Per-run bearer token

- JWT, HS256, service-internal signing key derived via HKDF(MASTER_KEY, "run-jwt").
- Claims: `run_id`, `org_id`, `exp = now + timeout_sec + 120s`, `secret_refs = [names]`.
- Only the **SHA-256 of the token** is persisted (in `run.runner_token_hash`).
  The token itself lives in the runner's env and process memory; any leak of
  the DB cannot replay.
- Every `/internal/runs/{id}/...` request validates: bearer present →
  `sha256(bearer) == runner_token_hash` → JWT `run_id` matches URL path →
  `exp` in future → JWT sig valid.
- `GET /internal/secrets` additionally checks every requested name is in the
  JWT's `secret_refs`. A runner cannot request secrets it was not authorized
  for, even with a valid token.

### Secret lifecycle

- **At rest:** envelope-encrypted. Per-secret 32-byte DEK generated on write;
  `dek_wrapped = AES-256-GCM(MASTER_KEY, DEK)`,
  `ciphertext = AES-256-GCM(DEK, value)`. Nonces are random per operation,
  stored alongside, never reused.
- **On read:** `cronfoundry serve` decrypts server-side and returns cleartext
  to the runner over localhost HTTP. Values exist briefly in the runner's
  process memory, are never written to `run_event.payload_json`, and are
  redacted from all logs.
- **Rotation:** creating a secret with an existing name overwrites ciphertext
  and bumps `version`. Prior values are gone. MVP has no undo.
- **Master key rotation:** `cronfoundry admin rotate-master-key` re-wraps
  every DEK. Design supports it; operator-facing subcommand deferred until a
  concrete rotation need.

### Logging and redaction

- `cronfoundry serve` at startup wraps `os.Stderr` in the P1 redacting writer.
- Before dispatching a run, the dispatcher loads the run's secret values into
  the process-wide redactor; after the run terminates, values are removed.
- slog's default handler emits through the redactor. `slog.Error("...", "err",
  err)` cannot leak a value whose substring the redactor knows.
- `POST /internal/runs/{id}/events` validates event payloads contain no
  active secret substring before committing — a defensive second line.

### Network posture

- `cronfoundry serve` binds `127.0.0.1:8080` by default. Runner processes on
  the same host reach it via loopback.
- No TLS for the loopback API. Operator threat model is single-host.
- Remote access (e.g., operator's workstation reaching into a home server) is
  via SSH forward or a reverse proxy the operator configures. Public exposure
  is not a P2 concern.
- Runner egress: GitHub API + git clone, LLM providers, destination webhooks.
  Scheduler and API have the same network surface as the runner since they
  share the process tree.

### Out of scope for P2 security

- Human authentication, sessions, allowlists — P3.
- CSRF, rate-limiting, request-IP validation — require human traffic.
- Multi-tenant isolation enforcement — schema ready, tests and row-level
  security come with real multi-tenancy.
- Key Vault / HSM — P4.

## Tech Stack

- **Go 1.25** (inherited from P1; unchanged).
- **Postgres 16**, driver `pgx`, migrations `goose` (embedded in binary, run
  at `serve` startup).
- **SQL generation**: `sqlc` for typed query functions. Queries live in
  `internal/db/queries/*.sql`.
- **Cron parsing**: `github.com/robfig/cron/v3`.
- **GitHub App auth**: manual JWT signing (crypto/rsa + stdlib) so we don't
  pull in a heavy GitHub helper dependency. Installation-token HTTP calls
  reuse `net/http`.
- **HTTP router**: stdlib `net/http` with `http.ServeMux` (Go 1.22+ pattern
  routing is enough for `/internal/runs/{id}/events`-shaped paths).
- **JWT**: `github.com/golang-jwt/jwt/v5` for per-run tokens.
- **Logging**: P1's `slog` + redactor, unchanged.
- **Tests**: `testing` + `testify`; integration tests use
  `github.com/testcontainers/testcontainers-go` for a throwaway Postgres.

No new cloud dependencies in P2. All Azure SDK usage stays in `internal/llm`
where P1 put it; P4 adds `internal/cloud/azure/` parallel implementations.

## Deployment (local dev)

Repo ships a `deploy/docker-compose.yml`:

```yaml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: cronfoundry
      POSTGRES_PASSWORD: cronfoundry
      POSTGRES_DB: cronfoundry
    ports: ["5432:5432"]
    volumes: ["db-data:/var/lib/postgresql/data"]

  app:
    image: cronfoundry:dev          # built locally via `make build-image`
    environment:
      CRONFOUNDRY_DATABASE_URL: postgres://cronfoundry:cronfoundry@db:5432/cronfoundry
      CRONFOUNDRY_MASTER_KEY: ${CRONFOUNDRY_MASTER_KEY}
      CRONFOUNDRY_GITHUB_APP_ID: ${CRONFOUNDRY_GITHUB_APP_ID}
      CRONFOUNDRY_GITHUB_APP_PEM: /run/secrets/app.pem
    ports: ["8080:8080"]
    depends_on: [db]
    secrets: [app_pem]

secrets:
  app_pem: { file: ./app.pem }
volumes:
  db-data:
```

A `Makefile` provides `make dev / make test / make integration / make migrate
/ make build`. First-time setup is:

1. `make build && docker-compose up -d db`
2. `CRONFOUNDRY_DATABASE_URL=... cronfoundry admin init` (generates master
   key, writes `.env.local`, creates organization row)
3. Register the GitHub App via GitHub's web UI (one-time; use the manifest
   shipped under `deploy/github-app-manifest.json`).
4. `docker-compose up` — full service running.
5. `cronfoundry admin connect-repo <owner/name> --installation-id X`
6. `cronfoundry admin set-secret <name>` (stdin reads the value).
7. `cronfoundry admin trigger <skill-path> <schedule-name>` for the first run.

## MVP Scope — in / out

### In

- Postgres schema (8 tables) + goose migrations embedded in binary.
- Three concurrent loops inside `cronfoundry serve`: sync poller, scheduler,
  dispatcher.
- `/internal` HTTP API: run context, secrets (scoped by JWT claim),
  clone-url, events, finalize, manual trigger.
- Dual-mode `cronfoundry runner` (HTTP mode for P2 use, standalone CLI for
  P1-compat / smoke testing).
- GitHub App authentication (JWT + installation tokens + 50-min cache).
- Repo sync via HEAD-first polling (default 60s, configurable per connection).
- Cron scheduling with per-schedule timezone and overlap policies
  (`skip`/`queue`/`concurrent`).
- Envelope-encrypted secret store (master key in env, per-secret DEKs).
- Audit log populated for system actions (sync enable/disable, manual
  trigger). Human-action audit entries await P3.
- `cronfoundry admin` subcommands: init, connect-repo, set-secret,
  list-schedules, trigger.
- `docker-compose.yml`, `Makefile`, end-to-end integration test.

### Deferred (with destinations)

| → P3 (UI) | → P2.5 | → P4 (Azure) | Fast-follow (post-MVP) |
| --- | --- | --- | --- |
| Web UI | GitHub webhook receiver | Container Apps Jobs dispatcher | Copilot Enterprise provider |
| GitHub OAuth for humans | Public API endpoints | Key Vault secret store | MCP tool support |
| User / role tables | Multi-account GitHub Apps | Bicep templates | Auto-pause on repeated failures |
| Live-tail over websockets |  | GHCR image publishing | Rich destination formatting |
| UI secret CRUD |  | Azure Monitor integration | Conditional routing |

## Plan Decomposition

P2 is split into four sub-plans, executed in order. Each produces working,
independently-testable software.

- **P2a — Data layer.** Postgres schema + goose migrations + pgx pool +
  sqlc queries for all eight tables + envelope-encrypted `secretstore` +
  `cronfoundry admin init` / `admin set-secret`. Tested via docker-compose
  Postgres + integration suite. ~15 tasks.
- **P2b — GitHub + sync.** App JWT signing + installation-token cache +
  clone helpers + sync poller (Loop 1) + `cronfoundry admin connect-repo`.
  Tested against a fixture repo hosted on a real GitHub App. ~12 tasks.
- **P2c — Scheduler + API + runner HTTP mode.** Scheduler tick + overlap
  policies + subprocess dispatcher + JWT sign/verify + `/internal/*`
  endpoints + runner HTTP client + events/finalize roundtrip. This is the
  heaviest sub-plan. ~20 tasks.
- **P2d — Admin CLI + integration.** `cronfoundry admin list-schedules`,
  `trigger`, `rotate-master-key` (skeleton). `docker-compose.yml`. `Makefile`.
  End-to-end integration test that exercises the success criteria top to
  bottom. ~10 tasks.

Total ~55–60 tasks, mirroring P1's 22 for a correspondingly larger surface.

## Success Criteria

A self-hoster should be able to, in one session:

1. `git clone cronfoundry && make build && docker-compose up -d db`
2. Register their own GitHub App (one-time, via GitHub web UI, manifest
   provided).
3. `cronfoundry admin init` — generates master key, seeds organization,
   writes `.env.local`.
4. `docker-compose up` — service running.
5. `cronfoundry admin connect-repo myorg/skills-repo --installation-id 12345`.
6. `cronfoundry admin set-secret slack_webhook` (pastes URL).
7. `cronfoundry admin set-secret openai_api_key` (pastes key).
8. Wait ≤ 60s; first sync completes, skills appear in
   `cronfoundry admin list-schedules`.
9. `cronfoundry admin trigger skills/weekly-digest monday-morning` — run
   fires, Slack message lands, memory.md commit pushes to repo.
10. Let a scheduled cron boundary pass; observe the run fires automatically
    without intervention.

If that loop works end-to-end and is boring to operate, P2 has delivered.

## Risks & Open Questions

- **GitHub App registration UX.** One-time, but fiddly for a single-user
  self-host. Mitigations: ship a `deploy/github-app-manifest.json` and a
  walkthrough in the README. If friction is real, P2.5 can add a
  `cronfoundry admin register-app` subcommand that automates the manifest
  flow.
- **Polling lag vs. webhooks.** Default 60s sync means a push → scheduled
  fire round-trip can be up to 60s stale. Acceptable for the localhost
  use case; if users ask for faster, P2.5 adds webhook support.
- **Single-process failure domain.** In P2, API + scheduler share a process.
  An API handler panic that escapes recovery could take the scheduler down
  with it. Go's stdlib HTTP server does per-request `recover()`, so this
  should be rare. If it ever hurts, split into two binaries — the package
  layout already supports it.
- **Master key in env.** Loses rotation atomicity (operator must restart all
  processes after a rotate). Good enough for MVP. P4's Key Vault answers
  this by holding the master key out-of-process.
- **Running more than one `cronfoundry serve` instance.** Idempotent
  ticks keep duplicate-fire safe via `UNIQUE (schedule_id, fire_time)`, but
  two sync loops cloning the same repo wastes bandwidth. Acceptable for MVP.
  Leader election is a P4+ concern once horizontal scale matters.

## References

- [P1 design](./2026-04-19-cronfoundry-design.md)
- [PRD](./2026-04-19-cronfoundry-prd.md)
- [P1 plan — shipped as tag `v0.1.0-p1`](../plans/2026-04-19-p1-core-runner.md)
