---
description: Run a live Fly.io dogfood pass — deploy fresh env, surface bugs, fix → tag → roll → verify autonomously
allowed-tools: Bash, Read, Edit, Write, Grep, Glob, Agent
---

# Fly dogfood round

> Status: this variant has not been exercised end-to-end yet. Treat the
> Fly-specific commands and sharp edges below as best-effort transcribed
> from the Azure-tested playbook. Capture corrections back into this file.

Run `bash scripts/fly-quickstart.sh --non-interactive` against a real
Fly.io organization, then `bash scripts/fly-smoke-assert.sh`, surface
every bug they hit, get fixes merged + tagged + deployed, and exit only
when a smoke run completes end-to-end:

- A run with `Status: Success`, non-zero token counts on
  `https://$FLY_API_APP.fly.dev/`.
- An issue filed in `$CRONFOUNDRY_SKILLS_REPO` titled `smoke run <id>`.
- A `chore(cronfoundry): update memory.md from run <id>` commit on the
  skills repo's default branch.

## Current state (auto-loaded)

- Repo: !`pwd`
- Branch: !`git branch --show-current`
- Latest tag: !`git fetch --tags origin 2>/dev/null; git tag --sort=-v:refname | head -3 | tr '\n' ' '`
- Commits since latest tag: !`LATEST=$(git tag --sort=-v:refname | head -1); git log "$LATEST..origin/main" --oneline 2>/dev/null | head -10`
- Fly app names from .env: !`grep -E '^(FLY_API_APP|FLY_RUNNER_APP|CRONFOUNDRY_SKILLS_REPO)=' .env 2>/dev/null`
- Existing fly apps with cronfoundry prefix: !`flyctl apps list 2>/dev/null | grep -E '^(NAME|cronfoundry)' | head -10`
- Live model in skills repo: !`SKILLS_REPO=$(grep -E '^CRONFOUNDRY_SKILLS_REPO=' .env 2>/dev/null | cut -d= -f2 | tr -d '"'); [ -z "$SKILLS_REPO" ] && SKILLS_REPO=gambtho/skills; gh api repos/$SKILLS_REPO/contents/cronfoundry.yaml -H "Accept: application/vnd.github.raw" 2>/dev/null | grep -E '^\s*model:' | head -3`

## Fly context

- Repo: `/home/tng/workspace/cronfoundry`. Always work in a worktree
  under `.claude/worktrees/<topic>`. Never push to `main`.
- API app: `$FLY_API_APP` (default `cronfoundry-api`)
- Runner app: `$FLY_RUNNER_APP` (default `cronfoundry-runner`)
- Postgres: `cronfoundry-db`
- Public URL: `https://$FLY_API_APP.fly.dev/`
- Image: `ghcr.io/gambtho/cronfoundry` (single binary; runner subcommand)
- Skills repo: `$CRONFOUNDRY_SKILLS_REPO` — `cronfoundry.yaml` on the
  default branch
- Secrets: `.env` at repo root. `fly-quickstart.sh` will prompt for any
  missing keys and persist them back; under `--non-interactive` it
  dies on missing keys instead.

## Universal process and sharp edges

@docs/dogfood/shared.md

## Fly-specific commands

### Run quickstart + smoke assert

```bash
bash scripts/fly-quickstart.sh --non-interactive
bash scripts/fly-smoke-assert.sh
```

For an upgrade-only round (no fresh deploy), skip quickstart and run
only `fly-smoke-assert.sh` after the new image is deployed.

### Pull runner-side logs

```bash
flyctl logs --app "$FLY_RUNNER_APP" | tail -200
```

### Roll fly to a new tag

All three values must move together — see "image drift" in the shared
sharp edges:

```bash
TAG=X.Y.Z
IMAGE="ghcr.io/gambtho/cronfoundry:$TAG"
flyctl secrets set --app "$FLY_API_APP" "FLY_RUNNER_IMAGE=$IMAGE"
flyctl deploy --config deploy/fly/fly.api.toml    --app "$FLY_API_APP"    --image "$IMAGE"
flyctl deploy --config deploy/fly/fly.runner.toml --app "$FLY_RUNNER_APP" --image "$IMAGE" --no-ha
```

Or simply re-run
`scripts/fly-quickstart.sh --image=$IMAGE --non-interactive`, which
does all three in one batch.

### Re-run the smoke

```bash
bash scripts/fly-smoke-assert.sh
```

Iterate until success. Confirm the issue + writeback commit on the
skills repo after success.

## Fly-specific sharp edges

- **Image-drift pointer is `FLY_RUNNER_IMAGE`.** Secret on the api app;
  the dispatcher reads it when spawning runner executions.
  `fly-quickstart.sh` always sets all three values together — only
  matters if you `flyctl deploy` one app by hand.

- **`flyctl secrets set` triggers a rolling restart.** Batch in one
  call. `fly-quickstart.sh` already does this — only matters if you
  add secrets by hand between runs.

- **Postgres attach is one-shot.** Skip on `DATABASE_URL` already
  present, or attach fails with "already attached". `fly-quickstart.sh`
  handles this.

- **`.env` rewrite.** `fly-quickstart.sh` appends generated
  `CRONFOUNDRY_MASTER_KEY` and `CRONFOUNDRY_RUNNER_API_KEY` to `.env`
  on first run. `.env` is a **local runtime file only** — it's
  gitignored and must stay that way. **Never commit secrets to
  version control.** If multiple operators need the same keys,
  generate them once and distribute through a secret manager (Vault,
  1Password, AWS/Azure/GCP Secrets Manager) or another secure
  out-of-band channel (encrypted message, shared password manager).

- **`--fresh` is irreversible.** Destroys api, runner, AND Postgres
  (data loss). Interactive runs require typing `destroy`; under
  `--non-interactive` it proceeds without prompt — be sure before
  automating it.

- **Stale-run baseline.** `fly-smoke-assert.sh` snapshots the latest
  run id at startup and waits for a strictly newer run. If you re-run
  the assert without dispatching a new run, it will time out — that's
  correct behaviour. Click Run-now or wait for the cron between assert
  invocations.

- **`/api/runs` and `/api/runs/{id}` are session-gated.**
  `fly-smoke-assert.sh` currently calls them unauthenticated. If they
  401 in your env, surface the curl error and ask the operator how
  to authenticate (OAuth cookie or session token) — don't silently
  treat 401 as "no runs yet."
