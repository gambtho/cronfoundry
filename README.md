# CronFoundry

Self-hostable, GitOps-style scheduler for LLM skills. Runs a skill against
OpenAI / Anthropic / Azure AI Foundry on a schedule, publishes the output to
GitHub issues, Slack, Discord, or Teams, and commits learnings back to the
skill repo.

**Status:** `MVP shipped — deployable to Azure`. Includes always-on scheduler,
GitHub App sync, `/internal` HTTP API, subprocess + Container Apps Jobs runner
dispatch, React operator UI with live-tail logs, push-webhook resync, audit
log, persistent user table, and a one-command Bicep deploy. See
[`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`](docs/superpowers/specs/2026-04-19-cronfoundry-design.md)
for the design and [`docs/guides/smoke-test-mvp-azure.md`](docs/guides/smoke-test-mvp-azure.md)
for the Azure runbook.

## Requirements

- **Go 1.25 or later** (required by `github.com/openai/openai-go/v3`).
- Git working tree (the skill repo the runner operates on must be a git repo —
  writeback commits through `go-git`).
- An API key for one of: OpenAI, Anthropic, or Azure AI Foundry.
- An Incoming Webhook URL for at least one destination you want to publish to
  (Slack, Discord, Teams via Power Automate), or a GitHub PAT for the GitHub
  issue destination.

## Build

```bash
go build -o cronfoundry-runner ./cmd/runner
```

Produces a single ~25 MB static binary.

## Quick start (local dev)

```bash
# 1. Build.
make build

# 2. Generate a master key on first run, copy the env line it prints.
./cronfoundry admin init
export CRONFOUNDRY_MASTER_KEY='<paste>'

# 3. Start Postgres + cronfoundry (docker-compose).
cp .env.example .env.local   # edit with your values
# Place your GitHub App's private key at ./app.pem
make dev

# 4. Run migrations + seed the default organization.
export CRONFOUNDRY_DATABASE_URL='postgres://cronfoundry:cronfoundry@localhost:5432/cronfoundry?sslmode=disable'
make migrate

# 5. Connect a repo + set secrets via the CLI.
./cronfoundry admin connect-repo myorg/skills-repo --installation-id 12345
echo -n 'https://hooks.slack.com/...' | ./cronfoundry admin set-secret slack_webhook
echo -n 'sk-...' | ./cronfoundry admin set-secret openai_key

# 6. Watch logs.
cd deploy && docker compose logs -f cronfoundry
```

See [`docs/guides/smoke-test-p2.md`](docs/guides/smoke-test-p2.md) for the full walkthrough with GitHub App registration.

After deploying and connecting a GitHub App, set up the push webhook — see [`docs/webhook-setup.md`](docs/webhook-setup.md) for step-by-step instructions.

## Quick start (standalone runner)

A tiny end-to-end run with the bundled smoke fixture:

```bash
export OPENAI_API_KEY='sk-...'
export CRONFOUNDRY_SECRET_SLACK_URL='https://hooks.slack.com/...'

./cronfoundry-runner run \
  --repo ./testdata \
  --manifest cronfoundry.yaml \
  --skill-path skills/weekly-digest \
  --schedule-name monday-morning \
  --skip-push
```

The runner will:

1. Parse `testdata/cronfoundry.yaml` and resolve the named schedule.
2. Load `testdata/skills/weekly-digest/SKILL.md`, inline `{{ include "..." }}`
   directives, and build the final prompt.
3. Stream a completion from OpenAI with your key.
4. Strip any `<memory>…</memory>` block from the output.
5. POST the remaining text to the Slack webhook (isolated retries).
6. If a `<memory>` block was present, append it to `memory.md` and commit
   as `cronfoundry[bot]`. `--skip-push` keeps it local.
7. Print a JSON run summary to stdout.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--repo` | `.` | Repo root containing the manifest and skill files |
| `--manifest` | `cronfoundry.yaml` | Manifest path (relative to `--repo`) |
| `--skill-path` | *(required)* | Skill path as declared in the manifest |
| `--schedule-name` | *(required)* | Schedule name within the skill |
| `--llm-key-env` | `OPENAI_API_KEY` | Env var name holding the LLM API key |
| `--llm-endpoint` | — | Azure AI Foundry endpoint URL |
| `--llm-deployment` | — | Azure AI Foundry deployment name |
| `--dry-run` | false | Skip publish + writeback; print output only |
| `--skip-push` | false | Perform writeback commit locally but don't push |

## Configuration format

### `cronfoundry.yaml` — the manifest

```yaml
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        timezone: America/Los_Angeles
        overlap_policy: skip       # skip | queue | concurrent
        timeout_sec: 600
        provider: openai           # openai | anthropic | azure-foundry
        model: gpt-4o-mini
        destinations:
          - github-issue:
              repo: myorg/reports
              title: "Digest — {{ run.date }}"
              labels: [digest]
          - slack:
              secret: slack_webhook
              text: "{{ output.truncated 35000 }}"
        writeback:
          enabled: true
          path: memory.md
          mode: append             # append | replace
        env:
          LOOKBACK_DAYS: "7"
          TEAM_NAME:
            secret: team_name      # resolved from CRONFOUNDRY_SECRET_TEAM_NAME
```

### `SKILL.md` — per-skill prompt

```markdown
---
name: weekly-digest
description: Aggregates last week's activity
max_tokens: 4000
---
You are writing a weekly digest.

Context:
{{ include "context/template.md" }}

Respond with a short summary, then a <memory>...</memory> block with one
short learning.
```

Only `{{ include "relative/path.md" }}` is supported inside the body — no
conditionals or loops. Paths are relative to the skill directory and may not
escape it.

## Secret resolution

Secrets referenced by `{ secret: name }` in the manifest resolve from
environment variables with the prefix `CRONFOUNDRY_SECRET_<UPPER(name)>`.

Example: `secret: slack_webhook` ⇒ `CRONFOUNDRY_SECRET_SLACK_WEBHOOK`.

The LLM API key itself comes from whatever env var `--llm-key-env` names
(default `OPENAI_API_KEY`). The writeback push (if enabled) uses `GITHUB_TOKEN`.

All known secret values are scrubbed from stderr (slog attrs, errors, and
direct prints) before emission.

## Destination templates

A small, fixed set of variables is available in destination `text`/`content`/
`title`/`body` fields:

| Variable | Value |
|---|---|
| `{{ output }}` | Full published output (memory block stripped) |
| `{{ output.truncated N }}` | Same, limited to N runes with an ellipsis |
| `{{ run.id }}` | Run UUID |
| `{{ run.date }}` | Run start date (`YYYY-MM-DD`, schedule's timezone) |
| `{{ run.started_at }}` | ISO-8601 timestamp |
| `{{ schedule.name }}` | Schedule name from the manifest |
| `{{ skill.name }}` | Skill name from SKILL.md frontmatter |

Unknown variables render as their literal form and emit a warning.

## Architecture

```
cronfoundry/
├── cmd/
│   ├── cronfoundry/              # server + admin CLI (cobra)
│   └── runner/                   # one-shot per-fire runner
└── internal/
    ├── api/                      # /internal HTTP endpoints (runner-facing)
    ├── cloud/                    # Azure Container Apps Jobs dispatcher
    ├── config/                   # cronfoundry.yaml + SKILL.md parsers
    ├── db/                       # pgx + goose migrations + sqlc queries
    ├── github/                   # App JWT, install tokens, clone/commit
    ├── githubtest/               # test fixtures for github/
    ├── llm/                      # OpenAI / Anthropic / Azure Foundry
    ├── memory/                   # <memory>...</memory> parser
    ├── publish/                  # github-issue / slack / discord / teams
    ├── redact/                   # secret-value scrubber for logs
    ├── runner/                   # orchestration (load → LLM → publish)
    ├── scheduler/                # cron tick loop + overlap + sweep
    ├── secrets/                  # env-based secret resolver (runner-local)
    ├── secretstore/              # Azure Key Vault wrapper (server-side)
    ├── sync/                     # GitHub repo → skill/schedule sync
    ├── template/                 # destination-template renderer
    ├── testdb/                   # testcontainers Postgres boot helper
    ├── token/                    # per-run bearer JWT signer/verifier
    ├── webapi/                   # /api handlers for the React UI
    └── writeback/                # go-git commit + push
```

A run's status is one of `succeeded`, `partial_failure` (publish or writeback
failure), or `failed` (load/LLM error). Per-destination failures are isolated
— one broken webhook does not prevent other destinations from publishing.

## End-to-end tests

`make e2e` runs the `TestE2E_*` suite under the `e2e` build tag. It boots
throwaway Postgres containers via testcontainers and stubs the LLM,
Slack/Discord webhooks, and the git clone, so no external network or real
credentials are required — but Docker must be running locally.

CI runs the same target on every PR and on pushes to `main`.

## Design & spec

- Technical design: [`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`](docs/superpowers/specs/2026-04-19-cronfoundry-design.md)
- Product requirements: [`docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`](docs/superpowers/specs/2026-04-19-cronfoundry-prd.md)
- Implementation plans: [`docs/superpowers/plans/`](docs/superpowers/plans/)

## Roadmap

- **MVP** (this release) — Core runner, scheduler, GitHub sync, Key Vault,
  React UI with live-tail logs, push webhook, audit log, user management,
  Azure Bicep deploy. ✅
- **Deferred** — see the "Deferred" section of the
  [design spec](docs/superpowers/specs/2026-04-19-cronfoundry-design.md) for
  the ordered backlog (MCP tool support, Copilot Enterprise provider,
  auto-pause on consecutive failures, etc.).

### Operator endpoints

- `POST /webhook/github` — GitHub App push webhook; requires
  `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET` (see [`docs/webhook-setup.md`](docs/webhook-setup.md))
- `GET /api/audit` — admin-only audit log of every mutating API call
- `GET/POST/PATCH/DELETE /api/users` — admin user management backed by the
  `app_user` table; env vars `CRONFOUNDRY_ADMIN_LOGINS` /
  `CRONFOUNDRY_VIEWER_LOGINS` seed the table on first startup, then UI edits
  win
- Per-run `manifest.set`, `secret.fetched`, and `secret.denied` events emitted
  on the run timeline so operators can see exactly which KV entries each run
  touched
- `GET /api/runs/{id}/events/stream` — SSE stream consumed by the `LogTail`
  component in the Runs detail drawer for in-flight runs

### CSRF & origin allowlist

Set `CRONFOUNDRY_PUBLIC_BASE_URL` to the externally-reachable URL of the
service (scheme+host, e.g. `https://cronfoundry.example.com`). The CSRF
middleware uses this as the allowlist for the `Origin`/`Referer` check. In
dev (no env var), the origin check is disabled; the `cf_csrf` cookie +
`X-CSRF-Token` header double-submit check still runs.

## License

MIT — see [LICENSE](LICENSE).
