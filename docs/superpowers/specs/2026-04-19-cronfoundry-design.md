# CronFoundry — Design

**Status:** Shipped (fd91fd6)
**Date:** 2026-04-19
**Author:** gambtho (brainstormed with Claude)

## Overview

CronFoundry is an Azure-native, GitOps-style scheduler for LLM "skills."
Users connect a GitHub repo containing skill prompts and a schedule manifest;
CronFoundry fires each schedule on cron, runs the prompt against a chosen LLM
provider, fans the output to configured destinations (GitHub issue, Slack,
Discord, Teams), and optionally commits learnings back to the repo.

The design target for MVP is a **single-tenant self-hostable service** with a
tenant-aware data model, so a hosted multi-tenant version can be added later
without a rewrite. The primary user is an engineer who is comfortable reviewing
YAML in GitHub PRs.

## Goals

- Reliable per-fire execution with strong isolation (one run cannot affect another).
- GitOps as the source of truth — skill prompts and schedule config live in the
  user's GitHub repo; the web UI is read-only for config and writes only for
  secrets and repo connections.
- First-class support for LLM providers the user actually has: OpenAI, Anthropic,
  Azure AI Foundry at MVP; GitHub Copilot Enterprise deferred to fast-follow.
- Clean self-hosting story: one Bicep deployment, one GitHub App registration,
  container images pulled from a public registry.
- Don't foreclose future growth: MCP tool support, additional destinations,
  hosted multi-tenant SaaS, and multi-cloud deploys are all designed-around, not
  designed-in.

## Non-Goals

- **Not a general-purpose cron.** Skills are LLM invocations plus a narrow
  write-back primitive. Arbitrary shell execution is out of scope.
- **Not an LLM gateway.** No caching, routing, or fallback across providers. One
  schedule selects one provider and one model.
- **Not a workflow engine.** No DAGs, fan-in, or conditional steps. A run is a
  single LLM call whose output can be published to multiple destinations.
- **Not a prompt IDE.** Authoring and editing prompts happens in the GitHub
  repo, via normal PR review. The UI does not edit prompts.

## High-Level Architecture

### Components

| Component | Purpose | Deploy target |
| --- | --- | --- |
| **API + UI** | Dashboard, GitHub OAuth login, repo connections, secret management, run history, live-tail logs | Azure Container App (always-on, 1–2 replicas) |
| **Scheduler** | Cron tick loop; inserts `run` rows for due schedules; dispatches runner Jobs | Azure Container App (single replica) |
| **Runner** | One-shot per fire: resolves skill, calls LLM, publishes outputs, commits write-back | Azure Container Apps Job (fresh container per fire) |
| **Database** | Schedules, run history, KV references, repo metadata, user allowlist, audit log | Azure Database for PostgreSQL Flexible Server |
| **Secrets** | LLM API keys, webhook URLs, GitHub App private key, per-skill env vars | Azure Key Vault |
| **Logs** | Structured runner output, access logs, KV-access audit | Azure Log Analytics (via Container Apps integration) |
| **GitHub App** | Repo read, contents/issues write, push webhooks | External (one App per self-hoster) |

### Schedule-fire flow (hot path)

```
Scheduler tick → Postgres (due schedules)
  → dispatch Container Apps Job (runner) with RUN_ID
    → API.context (returns run manifest + scoped KV refs)
    → Key Vault (pull manifest-listed secrets via managed identity)
    → GitHub App (shallow clone skill repo at pinned SHA)
    → LLM provider (stream completion)
    → Parse <memory> block (if enabled)
    → Publish to destinations (parallel, isolated retries)
    → Commit write-back via GitHub App (if applicable)
    → API.finalize (run status, duration, tokens, cost)
```

### Tenancy

Every domain table has an `organization_id` column from day one. The MVP seeds
a single organization at deploy time; multi-tenant SaaS adds organization
provisioning + per-tenant quotas without schema changes.

## Data Model

### Postgres entities

```
organization         (id, name, created_at)
                     — singleton in MVP

user                 (id, org_id, github_login, role, created_at)
                     — role: admin | viewer
                     — allowlisted GitHub users (config or DB list)

repo_connection      (id, org_id, github_installation_id, owner, name,
                      default_branch, created_at, last_synced_at)

skill                (id, org_id, repo_id, path, name,
                      current_sha, frontmatter_json, updated_at)
                     — one row per discovered SKILL.md; re-synced on push

schedule             (id, org_id, skill_id, cron, timezone,
                      overlap_policy, enabled, timeout_sec,
                      provider, model, keyvault_ref_llm_key,
                      destinations_json,
                      env_secret_refs_json,       -- {NAME: kv_ref}
                      writeback_config_json,
                      created_at, updated_at)

run                  (id, org_id, schedule_id, skill_sha,
                      status, fire_reason, actor,
                      started_at, finished_at, duration_ms,
                      tokens_in, tokens_out, cost_cents,
                      error_kind, error_msg,
                      output_ref, writeback_commit_sha)
                     — status: pending | running | succeeded
                              | partial_failure | failed
                     — fire_reason: schedule | manual

run_event            (id, run_id, ts, level, event_type, payload_json)
                     — structured timeline rows: llm.start, llm.chunk.batched,
                       publish.slack.ok, publish.teams.fail, writeback.commit.ok

audit_log            (id, org_id, actor, action, target, ts, detail_json)
                     — every mutating action
```

**Not stored in Postgres:**

- Raw secret values — only Key Vault references (`vault_name/secret_name/version`).
- Full LLM transcripts — written to run-log files in Log Analytics.
- Large outputs — optional blob storage pointer in `run.output_ref`; MVP keeps
  last N lines in Log Analytics only.

## Configuration Formats

### `cronfoundry.yaml` (repo root)

```yaml
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        timezone: America/Los_Angeles
        overlap_policy: skip        # skip | queue | concurrent
        timeout_sec: 600
        provider: openai            # openai | anthropic | azure-foundry
        model: gpt-5.1
        destinations:
          - github-issue:
              repo: myorg/reports
              title: "Weekly digest — {{ run.date }}"
              labels: [digest, automated]
          - slack: { secret: slack_digest_webhook }
        writeback:
          enabled: true
          path: memory.md
          mode: append              # append | replace
        env:
          LOOKBACK_DAYS: "7"
          TEAM_NAME: { secret: team_name }

  - path: skills/incident-retro
    schedules: []                   # manual-trigger only
```

`secret:` values are Key Vault secret names resolved at run time. Schedules with
`schedules: []` are run-now-only from the UI.

### `SKILL.md` (per-skill, Claude-Code-skill-directory shape)

```markdown
---
name: weekly-digest
description: Aggregates last week's GitHub activity into a written summary
model_hint: gpt-5.1
max_tokens: 8000
writeback:
  block_format: xml                 # <memory>...</memory> parsed from output
---

You are writing a weekly engineering digest.

Context files:
{{ include "context/template.md" }}

Memory from prior runs:
{{ include "memory.md" }}

... (rest of prompt) ...
```

- Frontmatter values are defaults; schedule-level values override.
- `{{ include "path" }}` is the only template primitive — no logic, no loops.
  The file is read from the repo tree at the pinned SHA.

### Sync model

GitHub App push webhook → **API** receives the webhook, validates the HMAC
signature, and re-parses `cronfoundry.yaml` and referenced `SKILL.md` files →
upserts `skill` and `schedule` rows with the new SHA. The DB-held schedule
config is a **cache** of the YAML. The YAML is the source of truth. UI writes
are limited to repo connections and secret values — never to schedule config.

The API is the only component that talks to GitHub; the scheduler and runner
consume GitHub data indirectly (scheduler reads schedule rows from Postgres;
runner fetches installation tokens through an API `/internal` endpoint).

## Execution Flow

A single scheduled fire, end to end.

1. **Scheduler tick** (every 30s). Query: `schedules WHERE enabled AND
   next_fire_at <= now()`. For each due schedule, insert a `run` row in
   `pending` state, idempotent on `(schedule_id, fire_time)`, and update
   `next_fire_at`.

2. **Dispatch.** Scheduler calls Container Apps Jobs API:
   `POST /jobs/{runner}/start` with env `RUN_ID`, `SCHEDULE_ID`. Runner image
   tag is pinned per schedule (default `current`) so runtime upgrades don't
   break in-flight definitions.

3. **Runner startup.** Runner authenticates to the API with its managed
   identity and calls `GET /internal/runs/{id}/context`. API returns:
   - Skill repo coords + SHA
   - Merged frontmatter + schedule config
   - **Run-scoped secret manifest** (KV refs for LLM key, webhook URLs, env vars)
   - Destination configs
   - Writeback config

4. **Repo checkout.** Shallow clone at the pinned SHA via a short-lived GitHub
   App installation token (fetched through API). Checkout to ephemeral `/work`.
   Read `SKILL.md`, resolve `{{ include }}` preprocessor against the repo tree.

5. **Prompt assembly.** Env vars substituted. Final system + user messages
   built. Summary logged to `run_event` with secret values redacted.

6. **LLM call.** Streamed completion via the provider adapter. Chunks written
   to a run log file + batched `run_event` rows (time/token thresholds, not
   per-chunk). Retry policy: exponential backoff on 429/5xx, max 3 retries,
   total budget bounded by `timeout_sec`.

7. **Output parse.** Extract optional `<memory>...</memory>` block. Remaining
   text is the "published output." If writeback is enabled but no block is
   found, log a warning and do not commit.

8. **Publish fan-out.** For each destination, in parallel:
   - Resolve templated fields.
   - POST to destination.
   - Own retry (3× exponential backoff on 5xx/network; no retry on 4xx).
   - Own `run_event` success/fail row.
   - **Failures are isolated** — a broken Slack webhook doesn't prevent the
     GitHub issue from being filed.

9. **Write-back.** If a memory block is present:
   - Get a fresh GitHub App installation token.
   - Read the current file at the pinned SHA, apply `append` or `replace`,
     commit to the default branch with bot identity `cronfoundry[bot]`.
   - Commit message: `chore(cronfoundry): update {path} from run {run.id}`.
   - `writeback_commit_sha` recorded on the run row.

10. **Finalize.** Runner calls `POST /internal/runs/{id}/finalize` with status,
    duration, token counts, cost. Any destination or writeback failure →
    `partial_failure` (`succeeded` means no errors at all).

### Policies

- **Overlap policy** (per schedule): `skip` (default), `queue`, `concurrent`.
- **Timeouts**: per-run wall-clock, configurable up to 1 hour, default 10 min.
  Runner self-kills past the timeout.
- **Kill switch**: `schedule.enabled`. UI pause flips it; in-flight runs
  complete, no new fires occur until re-enabled.
- **Manual trigger**: UI "Run now" inserts a `run` row with
  `fire_reason=manual`, dispatches the same runner flow, audit-logged.
- **Orphan sweep**: on scheduler tick, any `pending` or `running` row older
  than `timeout_sec + grace` with no recent `run_event` is marked `failed`.

### Failure matrix

| Failure | Run status | Side effects |
| --- | --- | --- |
| LLM 429/5xx, retries exhausted | `failed` | No publishes, no writeback |
| Repo clone fails | `failed` | Same |
| Output OK, all destinations OK, writeback OK | `succeeded` | — |
| Output OK, one destination fails | `partial_failure` | Others publish, writeback proceeds |
| Output OK, writeback commit fails | `partial_failure` | Destinations publish, warning logged |
| Runner OOM / container killed | `failed` (via orphan sweep) | — |

## Output Destinations

Each destination is a publisher adapter with a small, focused config surface.

### `github-issue`

```yaml
- github-issue:
    repo: myorg/reports
    title: "Weekly digest — {{ run.date }}"
    body: "{{ output }}"
    labels: [digest, automated]
    assignees: [alice]
```

Uses the GitHub App installation token. Target repo must have the same App
installed — the UI surfaces a warning on first use if not. Body auto-truncates
at 64KB with a trailing truncation marker. Returned issue URL is recorded in
`run_event` for UI deep-linking.

### `slack`

```yaml
- slack:
    secret: slack_digest_webhook
    text: "{{ output.truncated 35000 }}"
```

Incoming Webhook (classic). Markdown-formatted text. Truncates to ~35KB with a
marker (Slack limit ~40KB). Rich Block Kit formatting is deferred.

### `discord`

```yaml
- discord:
    secret: discord_alerts_webhook
    content: "{{ output.truncated 1900 }}"
    username: CronFoundry
```

Incoming webhook. Discord markdown subset. 2000-char limit — for long digests,
pair with `github-issue` as primary and `discord` as a link notification.

### `teams`

Microsoft deprecated the legacy Office 365 Connectors for Teams webhooks in
late 2024 (EOL Dec 2025). The supported path is a **Power Automate flow** with
trigger "When a Teams webhook request is received" and action "Post adaptive
card in chat or channel." The flow produces a webhook URL; the user stores it
as a CronFoundry secret.

```yaml
- teams:
    secret: teams_engineering_webhook
    title: "Weekly digest"
    text: "{{ output.truncated 25000 }}"
```

The runner builds a minimal Adaptive Card payload server-side
(`type: AdaptiveCard`, body: `TextBlock` with title and text). Users do not
author card JSON.

### Shared semantics

**Templating** is deliberately minimal:

| Variable | Value |
| --- | --- |
| `{{ output }}` | Full published output text |
| `{{ output.truncated N }}` | Output truncated to N chars with marker |
| `{{ run.id }}` | Run UUID |
| `{{ run.date }}` | Run start date, `YYYY-MM-DD` (schedule's timezone) |
| `{{ run.started_at }}` | ISO 8601 timestamp |
| `{{ schedule.name }}` | Schedule name |
| `{{ skill.name }}` | Skill name |

No logic, no loops. Unresolved variables render as the literal `{{ unknown }}`
and emit a warning in `run_event`.

**Retries per destination**: 3 attempts, exponential backoff (1s, 4s, 16s).
4xx = no retry (config error). 5xx or network = retry.

**Secrets**: Values resolved via Key Vault at publish time. Runner scrubs
secret values from all `run_event` payloads (regex + known-secret list from
the run manifest).

**Per-destination audit**: Every attempt writes a `run_event`
(`publish.<type>.ok` or `.fail`) with non-secret context (issue URL, HTTP
status, error class).

## Security Model

### Identity boundaries

| Principal | Identity | Capabilities |
| --- | --- | --- |
| **End user (admin/viewer)** | GitHub OAuth session → `user` row | UI actions per role; all writes audit-logged |
| **API + UI** | Managed identity `cf-api` | Postgres RW; Key Vault `list` + `set` on user secrets (cannot `get` values — prevents value exfiltration on API compromise); KV `get` scoped to the single GitHub App JWT signing key (required to mint installation tokens); receives GitHub webhooks, reads repo contents, makes GitHub API calls |
| **Scheduler** | Managed identity `cf-scheduler` | Postgres RW for schedules/runs; dispatch runner Job via Container Apps Jobs API. No KV access. No GitHub access. |
| **Runner (per-job)** | Managed identity `cf-runner` | KV read (logged, manifest-advised); no Postgres connection (uses API `/internal` endpoints) |
| **GitHub App** | App JWT → per-install tokens | `contents` RW, `issues` W, scoped to installed repos |

### Per-run secret scoping

- Scheduler dispatches a Job with `RUN_ID`.
- Runner authenticates to API via managed identity, receives the **run-scoped
  secret manifest**: the list of KV refs this run is allowed to use.
- Runner fetches each KV secret by name/version. **All KV reads log to Log
  Analytics** keyed by `run.id`.
- A KV read outside the manifest is a detectable anomaly (audit review + alert
  rule), not a breach.
- **Cryptographic enforcement via a KV-proxy sidecar is deferred** — rely on
  logging for MVP.

### Secret lifecycle

- **Write**: UI → API → KV direct. Never transits Postgres. Postgres stores a
  KV reference, not the value.
- **Rotate**: UI "rotate" creates a new KV version. Schedules referencing by
  name (no version) pick it up on next run. Schedules can pin a version for
  stability.
- **Delete**: soft-delete in KV (purge-protect on), hard-purge after retention
  window. Audit-logged.
- **View**: The UI never displays secret values after creation. Users see
  metadata only (name, last updated, last used). Rotation UX = paste new value.

### Transport & isolation

- Inter-service traffic stays inside the Container Apps environment VNet.
- Only API/UI is public-facing (via Container Apps ingress with TLS or Azure
  Front Door).
- Runner job containers have ephemeral `/work`, no persistent volumes, no
  lateral access. Egress is open in MVP (static NAT for webhook allow-listing
  in downstream systems); per-run egress allow-listing is a hardening step.
- **CSRF**: standard double-submit cookie on mutating endpoints.

### Auth flow

- **UI login**: GitHub OAuth via the same GitHub App's `client_id` /
  `client_secret`. Session cookie is HttpOnly + Secure + SameSite=Lax, signed
  with a key from KV, 7-day idle timeout.
- **Allowlist**: config file or DB-backed list of GitHub logins and/or org
  slugs. Mismatch → friendly "not authorized for this instance" page (no
  information disclosure about existence).
- **Roles**: `admin` (full write) and `viewer` (read-only dashboard). First
  allow-listed login is seeded as admin at deploy; admins can grant/revoke.

## Tech Stack

### Languages and frameworks

- **Backend**: Go (1.22+). Static binaries, small container images, strong
  concurrency primitives, mature Azure SDK, semi-official/official LLM SDKs
  (`openai-go`, `anthropic-sdk-go`, `azopenai` in Azure SDK for Go).
- **Frontend**: React + Vite + TypeScript. shadcn/ui on Radix + Tailwind.
  TanStack Query for server state. Served as a static bundle from the API.
- **DB**: Postgres 16. Driver: `pgx`. Migrations: `goose`, embedded in the API
  binary and run on startup.
- **YAML + schema validation**: `sigs.k8s.io/yaml` + JSON Schema for
  `cronfoundry.yaml` and `SKILL.md` frontmatter.

### Service layout

```
/cmd
  api/           # HTTP, GitHub OAuth, UI static serving, /internal endpoints
  scheduler/     # cron tick loop, job dispatch
  runner/        # one-shot per-fire execution

/internal
  config/        # cronfoundry.yaml + SKILL.md parse + json-schema validation
  github/        # App JWT, installation tokens, clone/commit/issue helpers
  llm/           # provider adapters (openai, anthropic, azure-foundry)
                 # common streaming interface
  publish/       # destination adapters (github-issue, slack, discord, teams)
  kv/            # Azure Key Vault wrapper + scoped fetch
  db/            # pgx + embedded goose migrations + sqlc-generated queries
  model/         # shared domain types
  cloud/         # abstraction for job dispatch / secrets / identity
                 # (Azure impl only at MVP; boundary for future clouds)

/web             # React UI
/deploy          # Bicep templates + GitHub Actions workflows
/docs            # Setup guides, operator runbook
```

The `internal/cloud/` package is an explicit abstraction layer so later AWS /
GCP adapters (job dispatch = Container Apps Jobs → ECS Tasks / Cloud Run Jobs;
secrets = Key Vault → Secrets Manager / Secret Manager; identity = managed
identity → IAM / workload identity) slot in without changes to service code.

### Azure resources (single Bicep deployment)

| Resource | SKU / Config | Notes |
| --- | --- | --- |
| Resource Group | — | `rg-cronfoundry-{env}` |
| Container Apps Environment | Consumption + Workload Profiles | VNet-integrated |
| Container App `api` | 0.5 vCPU, 1GB, 1–2 replicas | Public ingress |
| Container App `scheduler` | 0.25 vCPU, 0.5GB, 1 replica | Internal only |
| Container Apps Job `runner` | 1 vCPU, 2GB default; per-schedule override | Manual-trigger type; dispatched by scheduler |
| Postgres Flexible Server | B1ms burstable, 1 vCPU, 2GB | Private Endpoint, no public access |
| Key Vault | Standard | Purge-protect on, soft-delete 30d |
| Log Analytics Workspace | 30d retention default | Container Apps logs route here |
| Container Registry | Basic (optional; GHCR is default) | For private image mirroring |
| Managed Identities ×3 | — | `cf-api`, `cf-scheduler`, `cf-runner`; scoped role assignments |

**Estimated idle/light-use cost**: ~$60–$90/month (Postgres B1ms ~$25; Container
Apps Environment base $0; Basic ACR $5 if used; KV ops negligible; Log
Analytics under free tier for small deploys; compute $20–$30 for light load).
Container Apps Jobs bill per vCPU-second — a weekly 2-minute digest is pennies
per month per schedule.

### CI/CD

- GitHub Actions in the CronFoundry repo: PR → build all images, `go test`,
  `go vet`, `golangci-lint`, web lint + build.
- Merge to `main` → push versioned tags to **GHCR** (public) and optionally to
  a self-hoster's ACR (mirrored via workflow).
- Self-hoster deploy:
  1. `az deployment sub create -f deploy/main.bicep -p params.json`
  2. Set `ghcr.io/cronfoundry/<service>:vX.Y.Z` image tags on the Container
     Apps / Jobs.
  3. Upgrade = bump image tag + redeploy.

### Observability

- **Run logs** → Log Analytics. UI surfaces the last N lines via a pass-through
  KQL query.
- **Metrics** → OpenTelemetry emits `run.duration`, `run.cost`, `run.tokens`,
  `publish.failure.count`, `scheduler.tick_lag`. Container Apps built-in metrics
  cover CPU / memory / request rate.
- **Alerting** → out of scope for MVP code, but docs include recommended Azure
  Monitor rules (consecutive failures > 5, scheduler tick stalled > 5 min,
  runner OOM rate, cost-per-run anomalies).

## MVP Scope

### In scope

**Runtime**

- Container Apps Jobs runner, per-fire isolation
- Scheduler with cron + timezone + overlap policies (`skip` default)
- Manual "Run now" trigger
- Default 10-min wall-clock timeout, per-schedule override up to 1 hour

**Config**

- GitOps: `cronfoundry.yaml` at repo root, many skills per repo
- `SKILL.md` with frontmatter + `{{ include }}` preprocessor
- GitHub App push webhook resync

**LLM providers**

- OpenAI, Anthropic, Azure AI Foundry (BYOK)
- Streaming completions
- Token / cost accounting per run
- Per-schedule provider + model selection

**Write-back**

- XML `<memory>...</memory>` block parsed from output
- `append` or `replace` modes
- Committed to default branch as `cronfoundry[bot]`

**Destinations**

- GitHub issue, Slack, Discord, Teams (Power Automate)
- Simple templating (above)
- Per-destination retry + isolation
- `partial_failure` as first-class run status

**Security**

- GitHub App, per-installation scoped tokens
- Key Vault for all secrets, DB holds references only
- Managed identities per service, run-scoped secret manifest (advisory, logged)
- GitHub OAuth login, admin/viewer roles, allowlist
- Audit log on mutations

**UI (read-only dashboard + minimum writes)**

- Connected repos view
- Skills + schedules view (YAML-discovered)
- Run history + per-run detail (status, timing, cost, output, destination results)
- Live-tail logs for in-flight runs
- Secret create/rotate (never view value)
- Run now, pause / resume schedule
- GitHub OAuth login

**Ops**

- Bicep deployment template + setup guide
- Multi-arch container builds, published to GHCR
- Postgres migrations at API startup
- Log Analytics integration
- One-command deploy docs

### Deferred (ordered by likely priority)

1. GitHub Copilot Enterprise provider (the OAuth-device-flow path)
2. MCP tool support in skills
3. Auto-pause on consecutive failures
4. Rich destination formatting (Block Kit, Adaptive Cards, structured multi-output skills)
5. Conditional destination routing (`on_failure:` / `on_success:`)
6. UI-managed schedules (per-skill `managed: yaml | ui`)
7. SSO beyond GitHub (Entra, generic OIDC)
8. Additional destinations (email, PagerDuty, custom HTTP)
9. KV-proxy sidecar for cryptographic per-run secret scoping
10. Helm / AKS deploy path
11. Multi-cloud (AWS + GCP adapters)
12. Hosted multi-tenant SaaS (billing, signup, quotas — data model already ready)
13. Image signing / SBOM

## Success Criteria

A user should be able to, in one afternoon:

1. Deploy CronFoundry to Azure with one Bicep command.
2. Register a GitHub App and paste its credentials into CronFoundry.
3. Install the App on a repo containing a `cronfoundry.yaml` and one `SKILL.md`.
4. Paste an OpenAI / Anthropic / Azure AI Foundry API key into CronFoundry.
5. Configure one Slack webhook secret.
6. See the first scheduled run fire and land as a GitHub issue + Slack message.
7. See a `memory.md` commit land on their repo from the bot identity.

If that loop works end-to-end and is boring to operate, the MVP has delivered.
