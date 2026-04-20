# CronFoundry

Self-hostable, GitOps-style scheduler for LLM skills. Runs a skill against
OpenAI / Anthropic / Azure AI Foundry on a schedule, publishes the output to
GitHub issues, Slack, Discord, or Teams, and commits learnings back to the
skill repo.

Status: **P1 — core runner CLI only.** The scheduler, API, web UI, and Azure
Bicep deployment are in later phases (P2–P4).

## Requirements

- **Go 1.25 or later.** (Driven by the `github.com/openai/openai-go/v3` dependency.)
- Git working tree (for writeback).
- An API key for one of: OpenAI, Anthropic, or Azure AI Foundry.

## Build

```bash
go build -o cronfoundry-runner ./cmd/runner
```

## Run the smoke fixture

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

## Secret resolution

Secrets referenced by `{ secret: name }` in the manifest resolve from
environment variables with the prefix `CRONFOUNDRY_SECRET_<UPPER(name)>`.

For example, `secret: slack_url` reads `CRONFOUNDRY_SECRET_SLACK_URL`.

## Design & spec

- Technical design: `docs/superpowers/specs/2026-04-19-cronfoundry-design.md`
- Product requirements: `docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`
- Implementation plans: `docs/superpowers/plans/`
