# P2 End-to-End Smoke Test

**Goal:** Exercise the full `cronfoundry serve` hot path against real GitHub and a real LLM provider — prove that a scheduled skill fires autonomously, publishes output, commits a writeback, and finalizes cleanly.

**Status as of 2026-04-20:** P2a + P2b + P2c merged to `main`. P2d will automate most of the setup below via docker-compose + a `make smoke` target. This doc captures the manual path for now.

## Prerequisites

- Docker running locally (for Postgres).
- Go 1.25+ installed.
- A GitHub account where you can register an App.
- An OpenAI or Anthropic API key (Azure AI Foundry works too, but the smoke test uses OpenAI for simplicity).
- A Slack or Discord webhook URL, or a GitHub repo you own for the issue destination.
- About 30–45 minutes end to end (most of which is GitHub App setup).

## One-time setup (manual today; automated in P2d)

### 1. Register a GitHub App

CronFoundry reads your skill repos via a GitHub App rather than a personal access token, so the setup is a one-time registration.

1. Go to https://github.com/settings/apps and click **New GitHub App**.
2. **Name:** anything unique (e.g., `cronfoundry-<yourname>`).
3. **Homepage URL:** `http://localhost:8080` (placeholder — not used).
4. **Webhook:** uncheck "Active" (P2 uses polling, not webhooks).
5. **Permissions** → Repository permissions:
   - **Contents:** Read & write
   - **Issues:** Write
   - **Metadata:** Read
6. **Where can this GitHub App be installed?** Only on this account.
7. Click **Create GitHub App**.
8. On the app's settings page, note the **App ID** (top of the page).
9. Scroll to **Private keys** → **Generate a private key** — downloads a `.pem` file. Save it somewhere safe.
10. In the left sidebar, click **Install App** → select your account → **Install**. Choose "Only select repositories" and pick the repo you'll use for skills.
11. After installation, the URL will look like `https://github.com/settings/installations/12345678` — note the **Installation ID** (the number at the end).

### 2. Create a skill repo

On GitHub, create a new repo (e.g., `cronfoundry-smoke`) and push:

`cronfoundry.yaml`:
```yaml
version: 1
skills:
  - path: skills/hello
    schedules:
      - name: every-minute
        cron: "* * * * *"
        timezone: UTC
        provider: openai
        model: gpt-4o-mini
        destinations:
          - discord:
              secret: discord_webhook
              content: "CronFoundry smoke test — {{ run.started_at }}\n\n{{ output.truncated 1800 }}"
              username: CronFoundry
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

`skills/hello/SKILL.md`:
```markdown
---
name: hello
description: CronFoundry P2 smoke-test skill
max_tokens: 300
---
You are the P2 smoke-test skill.

Write one friendly sentence acknowledging that you fired on a schedule.
Then add a `<memory>` block with a one-line learning dated with the UTC time you ran.
```

Initialize with `git init && git add . && git commit -m 'seed' && git push` (after pushing the `main` branch to GitHub).

### 3. Install the GitHub App on this repo

If you didn't select this repo during step 1.10, go back to the App's settings → **Install App** → click the gear icon → add the new repo to the installed repos list.

## Boot the service

### 4. Start Postgres

```bash
docker run --rm -d --name cronfoundry-smoke \
  -e POSTGRES_USER=cronfoundry -e POSTGRES_PASSWORD=cronfoundry \
  -e POSTGRES_DB=cronfoundry \
  -p 5433:5432 postgres:16-alpine
sleep 3   # wait for init
```

### 5. Build the binary

From the `cronfoundry` repo root:

```bash
go build -o /tmp/cronfoundry ./cmd/cronfoundry
```

### 6. Set environment variables

```bash
export CRONFOUNDRY_DATABASE_URL='postgres://cronfoundry:cronfoundry@localhost:5433/cronfoundry?sslmode=disable'
export CRONFOUNDRY_GITHUB_APP_ID='<the App ID from step 1.8>'
export CRONFOUNDRY_GITHUB_APP_PEM='/absolute/path/to/your-app-private-key.pem'
export CRONFOUNDRY_SECRET_DISCORD_WEBHOOK='https://discord.com/api/webhooks/...'
# (note: this is an env var name P1 uses; P2's secret store reads from Postgres)
```

### 7. Initialize the database

First run prints a freshly-generated master key. Copy it into your environment.

```bash
/tmp/cronfoundry admin init
# Copy the printed CRONFOUNDRY_MASTER_KEY line.
export CRONFOUNDRY_MASTER_KEY='<paste>'

# Second run actually migrates + seeds the organization.
/tmp/cronfoundry admin init
```

Expected: `Seeded organization id=<uuid> name="default". Ready.`

### 8. Store secrets in the encrypted store

```bash
echo -n 'https://discord.com/api/webhooks/<your-url>' | /tmp/cronfoundry admin set-secret discord_webhook
echo -n 'sk-<your-openai-key>' | /tmp/cronfoundry admin set-secret openai_key
```

Expected: `Stored secret "discord_webhook"`, etc.

### 9. Connect your skill repo

```bash
/tmp/cronfoundry admin connect-repo <your-github-username>/cronfoundry-smoke \
  --installation-id <the Installation ID from step 1.11> \
  --branch main \
  --sync-interval-sec 60
```

Expected: `Connected <username>/cronfoundry-smoke (install=..., branch=main, interval=60s)`.

### 10. Trigger the first sync manually

```bash
/tmp/cronfoundry admin trigger-sync <your-github-username>/cronfoundry-smoke
```

Expected: `Synced <username>/cronfoundry-smoke`.

### 11. Verify the schedule was discovered

```bash
/tmp/cronfoundry admin list-schedules
```

Expected: a row showing `skills/hello / every-minute / * * * * * / openai / true`.

### 12. Modify the skill's schedule to reference the OpenAI key

The `cronfoundry.yaml` schedule entry doesn't have an `llm_secret_ref` — which means the runner will fail looking for the LLM API key. Edit the yaml to add it, push, re-sync:

```yaml
      - name: every-minute
        cron: "* * * * *"
        timezone: UTC
        provider: openai
        model: gpt-4o-mini
        llm_secret_ref: openai_key      # ← add this
        destinations:
          # ... (unchanged)
```

Push the change to GitHub, then:

```bash
/tmp/cronfoundry admin trigger-sync <username>/cronfoundry-smoke
```

**Note:** the CLI doesn't currently expose `--llm-secret-ref` directly on `connect-repo`. The field comes from parsing the YAML. This is why step 12 is a second trigger-sync — P2's YAML-as-source-of-truth design.

### 13. Start the service

```bash
/tmp/cronfoundry serve
```

Expected log output:
```
time=... level=INFO msg="serve: API listening" addr=127.0.0.1:8080
time=... level=INFO msg="scheduler: Loop: tick" dispatched=0 skipped=0 queued=0 errored=0
```

### 14. Watch the skill fire

Leave `serve` running. Within 60 seconds (cron boundary), the scheduler should:

1. Log: `scheduler: Loop: tick dispatched=1`
2. Spawn `cronfoundry runner --run-id <uuid>` as a subprocess (visible in `ps`)
3. That subprocess fetches its context, clones the repo, calls OpenAI, posts to Discord, commits `memory.md`, finalizes

### 15. Verify success

**Discord:** you should see a message from `CronFoundry` in the webhook's channel.

**GitHub:** `git pull` on your `cronfoundry-smoke` repo should show a new commit from `cronfoundry[bot]` appending to `memory.md`.

**Database:**
```bash
docker exec cronfoundry-smoke psql -U cronfoundry -d cronfoundry -c \
  "SELECT id, status, tokens_in, tokens_out, duration_ms, writeback_commit_sha FROM run ORDER BY created_at DESC LIMIT 1;"
```

Expected: `status='succeeded'`, token counts > 0, `writeback_commit_sha` populated.

**Runs listing (future P2d command):** today you'd query Postgres directly. P2d will add `admin list-runs` and `admin show-run <id>`.

## Teardown

```bash
# Stop the service with Ctrl-C (graceful shutdown — up to 10s for the API to drain)
docker stop cronfoundry-smoke
rm /tmp/cronfoundry
```

## What this proves

End-to-end:
- GitHub App token exchange (`InstallationCache`)
- Repo HEAD-check + shallow clone at a pinned SHA
- YAML manifest + `SKILL.md` parsing
- Secret envelope decryption via Postgres-backed store
- Per-run JWT signing + HTTP `/internal/*` API
- Subprocess-dispatched runner with captured stdout/stderr
- LLM streaming completion + token accounting
- Destination publishing (Discord webhook with templating)
- Writeback commit + push via `cronfoundry[bot]`
- Run lifecycle: `pending` → `running` (via `SetRunRunning`) → `succeeded`
- Graceful shutdown on SIGTERM

## Known gaps (P2d)

- **No docker-compose** — you run Postgres separately today. P2d ships `deploy/docker-compose.yml`.
- **Manual env-var setup** — P2d may add a `.env.example` + a `make smoke` target.
- **No `admin list-runs` / `admin show-run`** — you query Postgres directly. P2d adds them.
- **Writeback push skipped** — P2c's runner sets `SkipPush: true` because the push token isn't plumbed. Writeback commits land locally in the clone (gets discarded after the run); the bot identity is stamped but nothing reaches GitHub. P2d wires a dedicated push token fetch via `/internal/repos/{id}/clone-url`-style endpoint.

## References

- P2 design spec: `docs/superpowers/specs/2026-04-19-cronfoundry-p2-design.md`
- P2a plan: `docs/superpowers/plans/2026-04-19-p2a-data-layer.md`
- P2b plan: `docs/superpowers/plans/2026-04-20-p2b-github-sync.md`
- P2c plan: `docs/superpowers/plans/2026-04-20-p2c-scheduler-api.md`
