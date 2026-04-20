# CronFoundry

Self-hostable, GitOps-style scheduler for LLM skills. Runs a skill against
OpenAI / Anthropic / Azure AI Foundry on a schedule, publishes the output to
GitHub issues, Slack, Discord, or Teams, and commits learnings back to the
skill repo.

**Status:** `P2 — Postgres + API + scheduler`. The web UI and Azure Bicep
deployment are in later phases (P3–P4). See
[`docs/superpowers/plans/`](docs/superpowers/plans/) for the roadmap.

## Requirements

- **Go 1.25 or later** (required by `github.com/openai/openai-go/v3`).
- Docker + Docker Compose (for the local dev harness).
- A **GitHub App** with read access to your skill repo, installed on the target
  owner/org. You'll need its App ID and a private key PEM file.
- An API key for one of: OpenAI, Anthropic, or Azure AI Foundry.
- An Incoming Webhook URL for at least one destination (Slack, Discord, Teams),
  or a GitHub PAT for the GitHub issue destination.

## Quick start (docker-compose)

### 1. Configure secrets

Copy the example env file and edit it:

```bash
cp .env.example .env.local
# Fill in CRONFOUNDRY_MASTER_KEY (or leave blank to auto-generate on first init),
# CRONFOUNDRY_GITHUB_APP_ID, CRONFOUNDRY_DB_PASSWORD.
```

Place your GitHub App private key at `deploy/app.pem` (gitignored).

### 2. Start the stack

```bash
make dev
```

This builds the image and starts Postgres + the CronFoundry service. The API
listens on `127.0.0.1:8080` by default.

### 3. Initialize the database and seed the org

```bash
# First run: generates a master key and runs migrations.
./cronfoundry admin init
# Copy the printed CRONFOUNDRY_MASTER_KEY into .env.local, then re-run:
./cronfoundry admin init
```

### 4. Connect a repo

```bash
./cronfoundry admin connect-repo owner/repo \
  --installation-id <GitHub App installation ID> \
  --branch main
```

### 5. Store secrets

```bash
echo 'https://discord.com/api/webhooks/...' | ./cronfoundry admin set-secret discord_webhook
echo 'sk-...'                               | ./cronfoundry admin set-secret openai_key
```

### 6. Sync the repo (fetch skills + create schedules)

```bash
./cronfoundry admin trigger-sync owner/repo
```

### 7. Trigger a manual run (optional smoke-test)

```bash
# Look up the schedule UUID:
./cronfoundry admin list-runs   # or query Postgres directly

curl -s -X POST http://127.0.0.1:8080/internal/schedules/<schedule-id>/run-now \
  -H 'Content-Type: application/json' -d '{}'
```

The scheduler tick runs every 30 seconds (configurable via `--tick-cadence`).
Skills fire automatically according to their cron expressions.

### 8. Tear down

```bash
make dev-down
```

## Build from source

```bash
make build
```

Produces `cronfoundry` (the server + admin CLI) in the project root.

## Environment variables (serve + admin)

| Variable | Required | Purpose |
|---|---|---|
| `CRONFOUNDRY_DATABASE_URL` | Yes | Postgres DSN (e.g. `postgres://user:pass@host/db`) |
| `CRONFOUNDRY_MASTER_KEY` | Yes (serve) | 32-byte hex key for envelope encryption of secrets |
| `CRONFOUNDRY_GITHUB_APP_ID` | Yes | GitHub App numeric ID |
| `CRONFOUNDRY_GITHUB_APP_PEM` | Yes | Path to GitHub App private key PEM |
| `CRONFOUNDRY_GITHUB_BASE_URL` | No | Override GitHub API base (testing) |
| `CRONFOUNDRY_GITHUB_CLONE_BASE` | No | Override clone URL base (testing) |

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
            secret: team_name
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

Secrets set via `cronfoundry admin set-secret <name>` are stored encrypted in
Postgres using envelope encryption (AES-GCM with a per-secret data key, wrapped
by `CRONFOUNDRY_MASTER_KEY`).

Secrets are referenced in `cronfoundry.yaml` with `{ secret: name }` objects:

```yaml
destinations:
  - discord:
      secret: discord_webhook   # resolved from the encrypted store
```

The LLM API key is set separately by updating `llm_secret_ref` on the schedule
row (P3 will expose this in the UI). All secret values are scrubbed from logs
before emission.

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
├── cmd/cronfoundry/               # CLI entry (cobra) — serve + admin subcommands
│                                  # runner subcommand invoked as subprocess by scheduler
├── deploy/
│   ├── Dockerfile                 # multi-stage distroless image (~25 MB)
│   ├── docker-compose.yml         # Postgres + cronfoundry serve (local dev)
│   └── docker-compose.test.yml    # ephemeral Postgres only (CI / e2e)
└── internal/
    ├── api/                       # /internal/* HTTP surface (runner ↔ serve IPC)
    ├── config/                    # cronfoundry.yaml + SKILL.md parsers
    │                              # {{ include }} preprocessor, validation
    ├── db/                        # sqlc-generated queries + migrations
    ├── github/                    # GitHub App install token cache
    ├── llm/                       # Provider interface + adapters:
    │                              #   openai, anthropic, azure-foundry (streaming)
    ├── memory/                    # <memory>...</memory> block parser
    ├── publish/                   # Publisher interface + parallel dispatcher
    │                              # github-issue, slack, discord, teams
    ├── runner/                    # skill orchestration:
    │                              #   load → LLM → memory parse → publish → writeback
    ├── scheduler/                 # tick loop, orphan sweep, subprocess dispatcher
    ├── secretstore/               # envelope-encrypted Postgres secret store
    ├── sync/                      # repo poller: HEAD check → clone → upsert schedules
    ├── template/                  # safe variable-set template renderer
    ├── token/                     # per-run JWT signer/verifier
    └── writeback/                 # go-git commit + optional push
```

A run's status is one of `succeeded`, `partial_failure` (publish or writeback
failure), or `failed` (load/LLM error). Per-destination failures are isolated —
one broken webhook does not prevent other destinations from publishing.

## Design & spec

- Technical design: [`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`](docs/superpowers/specs/2026-04-19-cronfoundry-design.md)
- Product requirements: [`docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`](docs/superpowers/specs/2026-04-19-cronfoundry-prd.md)
- Implementation plans: [`docs/superpowers/plans/`](docs/superpowers/plans/)

## Roadmap

- **P1** — Core runner CLI. ✅
- **P2** — Postgres + API + scheduler + admin CLI + docker-compose. ✅
- **P3** — React web UI with GitHub OAuth, read-only dashboard + secret CRUD.
- **P4** — Azure Bicep deployment, GHCR image publishing, CI/CD.

## License

TBD.
