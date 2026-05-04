# Fly dogfood round — repeating prompt

> Drop this whole file in as the first message of a new Claude Code
> session whenever we want to run another live fly.io dogfood pass.

## Goal

Run `bash scripts/fly-quickstart.sh --non-interactive` against a real
Fly.io organization, then `bash scripts/fly-smoke-assert.sh`, surface
every bug they hit, get fixes merged + tagged + deployed, and exit
only when a smoke run completes end-to-end:

- A run with `Status: Success`, non-zero token counts on
  `https://$FLY_API_APP.fly.dev/`.
- An issue filed in `$CRONFOUNDRY_SKILLS_REPO` titled
  `smoke run <id>`.
- A `chore(cronfoundry): update memory.md from run <id>` commit on the
  skills repo's default branch.

## Context the agent must load up front

1. **Repo:** `/home/tng/workspace/cronfoundry`. Always work in a
   worktree under `.claude/worktrees/<topic>`. Never push to `main`.
2. **Live env:** Fly.io org configured via `flyctl auth`. Apps:
   - API: `$FLY_API_APP` (default `cronfoundry-api`)
   - Runner: `$FLY_RUNNER_APP` (default `cronfoundry-runner`)
   - Postgres: `cronfoundry-db`
   - URL: `https://$FLY_API_APP.fly.dev/`
3. **Secrets:** in `.env` at repo root. `fly-quickstart.sh` will prompt
   for any missing keys and persist them back; under
   `--non-interactive` it dies on missing keys instead.
4. **Image layout (important — see drift bug below):** Every dispatcher
   invokes the runner via `cronfoundry runner …` on the main binary.
   Only one image (`ghcr.io/gambtho/cronfoundry`) is in active use.
5. **Skills repo:** `$CRONFOUNDRY_SKILLS_REPO`. Live config is
   `cronfoundry.yaml` on the default branch; current default model is
   `gpt-5-mini` (do NOT downgrade to `gpt-4o` — Copilot Enterprise
   rejects it).

## Process

For each round:

1. **Run quickstart + assert:**
   ```bash
   bash scripts/fly-quickstart.sh --non-interactive
   bash scripts/fly-smoke-assert.sh
   ```
   For an upgrade-only round (no fresh deploy), skip quickstart and run
   only fly-smoke-assert.sh after the new image is deployed.

2. **Watch for failure.** If the smoke run errors:
   - Pull the runner-side logs:
     ```bash
     flyctl logs --app "$FLY_RUNNER_APP" | tail -200
     ```
   - For 4xx/5xx from the LLM endpoint, the error format you want is
     `status=N body={...}` (`internal/llm/openai.go::chatErr`). If you
     only see the generic `400 Bad Request` form, the runner is on a
     stale image — see drift bug below.

3. **Fix in a worktree on a new branch.** Open a PR. Wait for merge.
   Never push to `main`. Never push to a branch that's already merged.

4. **Cut a tag** on `main` once the fix lands:
   ```bash
   git fetch origin main
   git tag -a vX.Y.Z <sha-on-main> -m "vX.Y.Z: <one-liner>"
   git push origin vX.Y.Z
   ```
   Tagging triggers `.github/workflows/release.yml`, which publishes
   `ghcr.io/gambtho/cronfoundry:VERSION`.

5. **Roll fly to the new tag.** All three values must move together
   or you'll hit the drift bug (see below):
   ```bash
   TAG=X.Y.Z IMAGE="ghcr.io/gambtho/cronfoundry:$TAG"
   flyctl secrets set --app "$FLY_API_APP" "FLY_RUNNER_IMAGE=$IMAGE"
   flyctl deploy --config deploy/fly/fly.api.toml    --app "$FLY_API_APP"    --image "$IMAGE"
   flyctl deploy --config deploy/fly/fly.runner.toml --app "$FLY_RUNNER_APP" --image "$IMAGE" --no-ha
   ```
   Or simply re-run `scripts/fly-quickstart.sh --image=$IMAGE
   --non-interactive`, which does all three in one batch.

6. **Re-run the smoke** via `scripts/fly-smoke-assert.sh`. Iterate
   until success.

## Known sharp edges (read these every round)

- **Image drift between api and runner.** `FLY_RUNNER_IMAGE` (secret
  on the api app) is what the dispatcher uses when it spawns runner
  executions. If you bump api but forget the secret, new dispatches
  keep running OLD code. `fly-quickstart.sh` always sets all three
  values together; only matters if you `flyctl deploy` one app by
  hand.

- **`cronfoundry-runner` image is dead.** All dispatchers invoke the
  `runner` subcommand on the main `cronfoundry` binary. The
  `-runner` image's entrypoint doesn't accept that subcommand. Never
  point `FLY_RUNNER_IMAGE` at `cronfoundry-runner:*` even though the
  name looks right. (See PR #83.)

- **`flyctl secrets set` triggers a rolling restart.** Batch in one
  call. fly-quickstart.sh already does this — only matters if you
  add secrets by hand between runs.

- **Postgres attach is one-shot.** Skip on `DATABASE_URL` already
  present, or attach fails with "already attached". fly-quickstart
  handles this.

- **Copilot model availability.** `gpt-4o` is not on the current
  Copilot Enterprise plan. `gpt-5-mini` works as a starter default.
  If you change models, patch the live
  `$CRONFOUNDRY_SKILLS_REPO/cronfoundry.yaml` via `gh api -X PUT`
  rather than re-deploying.

- **Sync interval.** The schedule sync runs roughly every 60s. After
  patching `cronfoundry.yaml`, wait one cycle (or look for an audit
  event) before clicking Run-now.

- **Stuck "Running" runs.** If a runner crashes before posting a
  terminal event, the dashboard shows the run as Running. The
  background orphan sweeper transitions it to `failed: orphan sweep:
  run exceeded timeout`. That's expected after a crash; just look at
  the runner-side logs to see the real error.

- **`.env` rewrite.** fly-quickstart appends generated
  `CRONFOUNDRY_MASTER_KEY` and `CRONFOUNDRY_RUNNER_API_KEY` to `.env`
  on first run. If `.env` is shared across operators, generate keys
  once and commit them out-of-band.

- **`--fresh` is irreversible.** It destroys api, runner, AND
  Postgres (data loss). Interactive runs require typing `destroy`;
  under `--non-interactive` it proceeds without prompt — be sure
  before automating it.

## Auto mode

This session is meant to run autonomously. Don't pause for
confirmation between fix → tag → deploy → re-run. Surface a question
only when blocked by a real ambiguity (model choice, plan-side
breakage, etc.).

## Output expected at the end of each round

A short report:

- New PRs opened/merged this round, with one-line descriptions.
- New tag(s) cut and what they contain.
- Final smoke run id + token counts + duration.
- Any new sharp edges discovered → update this prompt.
