<!--
Shared dogfood-round content — embedded into the platform-specific
slash commands (`/dogfood-azure`, `/dogfood-fly`) via `@` reference.

Anything specific to one platform (resource names, deploy commands,
log-pull commands, the env var that drives image drift) lives in the
slash command itself. Anything that's true regardless of platform
lives here.
-->

## Process (per round)

1. **Run the platform's quickstart and smoke.** Commands are in the
   slash command. For an upgrade-only round (no fresh deploy), skip
   the quickstart and just trigger a new run on an existing schedule.

2. **If the smoke run errors, pull runner-side logs first.** The
   exact command is platform-specific. For 4xx/5xx from the LLM
   endpoint, the error format you want is `status=N body={...}`
   (`internal/llm/openai.go::chatErr`). If you only see the generic
   `400 Bad Request` form, the runner is on a stale image — see
   "image drift" below.

3. **Fix in a worktree on a new branch. Open a PR. Wait for merge.**
   Never push to `main`. Never push to a branch that's already merged.
   If your PR was branched off un-squashed parent commits, after
   those parents land via squash-merge your branch can show conflicts
   on files you never edited; rebase to current `main` and
   cherry-pick only your real commits, then `git push --force-with-lease`.

4. **Cut a tag on `main` once the fix lands:**
   ```bash
   git fetch origin main
   git tag -a vX.Y.Z <sha-on-main> -m "vX.Y.Z: <one-liner>"
   git push origin vX.Y.Z
   ```
   Tagging triggers `.github/workflows/release.yml`. amd64 finishes
   in ~3–4 min and pushes `:VERSION` immediately, which is enough
   to roll the env. arm64 takes longer and merges later — don't wait.

5. **Roll the env to the new tag.** Platform-specific commands. The
   critical invariant on every platform: the dispatcher reads an env
   var or secret (`AZURE_CAE_JOB_IMAGE` on Azure, `FLY_RUNNER_IMAGE`
   on Fly) to know what image to spawn the runner with. If you bump
   the serve/api container but forget that pointer, **new dispatches
   keep running old code**. All three values must move together:
   serve image, runner-image pointer on serve, runner job/app image.

6. **Re-run the smoke.** Click "Run now" on the dashboard, or patch
   `cronfoundry.yaml` to a `*/2 * * * *` cron, wait ~2 min, then
   revert to the original schedule. Iterate until success.

## Cross-platform sharp edges

- **Image layout — only one binary is in active use.** Every
  dispatcher invokes the runner via `cronfoundry runner …` on the
  main binary at `ghcr.io/gambtho/cronfoundry`. The legacy
  `cronfoundry-runner` image's entrypoint doesn't accept that
  subcommand — never point your runner-image pointer at
  `cronfoundry-runner:*` even though the name looks right (PR #83).

- **Copilot model availability.** `gpt-4o` is not on the current
  Copilot Enterprise plan and will be rejected. `gpt-5-mini` works
  as a starter default. The current default is auto-loaded in Step 0
  of the slash command — don't downgrade. If you do change models,
  patch the live `cronfoundry.yaml` in the skills repo via
  `gh api -X PUT` rather than re-deploying.

- **Skills-repo SHA conflicts.** When patching `cronfoundry.yaml`
  via `gh api -X PUT`, GitHub returns `409 Conflict` on a stale
  SHA. Always re-fetch `.sha` immediately before each PUT — don't
  cache it across steps.

- **Schedule sync interval.** The schedule sync runs roughly every
  60s. After patching `cronfoundry.yaml`, wait one cycle (or look
  for an audit event) before clicking Run-now.

- **Stuck "Running" runs are crashes, not hangs.** If a runner
  crashes before posting a terminal event, the dashboard shows the
  run as Running. The background orphan sweeper transitions it to
  `failed: orphan sweep: run exceeded timeout`. Look at the
  runner-side logs for the real error rather than waiting it out.

- **Reset-and-prove for `next_fire_at` fixes.** To verify a sync-side
  fix end-to-end without re-bootstrapping the env, null
  `next_fire_at` on the **single** `schedule` row you're exercising
  (or change its cron/tz), then watch the next 60s sync cycle re-arm
  it. **Always scope the UPDATE** — bare
  `UPDATE schedule SET next_fire_at = NULL` clears every schedule in
  the org and breaks unrelated dispatchable jobs. Use the row's id
  or composite key:
  ```sql
  UPDATE schedule SET next_fire_at = NULL WHERE id = '<schedule-uuid>';
  -- or
  UPDATE schedule SET next_fire_at = NULL
   WHERE skill_id = '<skill-uuid>' AND name = '<schedule-name>';
  ```
  Cheaper than a full teardown and exercises the exact code path the
  bug originally hit.

- **Sandbox-blocked commands.** When `az`, `gh api`, `flyctl`, or
  anything else gets denied by the sandbox, surface the denial —
  don't silently fail. Ask the operator to approve.

## Auto mode

This session is meant to run autonomously. Don't pause for
confirmation between fix → tag → deploy → re-run. Surface a question
only when blocked by a real ambiguity (model choice, plan-side
breakage, an unfamiliar failure mode).

## Output expected at the end of each round

A short report:

- New PRs opened/merged this round, with one-line descriptions.
- New tag(s) cut and what they contain.
- Final smoke run id + token counts + duration.
- Any new sharp edges discovered. Add platform-specific ones to the
  relevant slash command file; add cross-cutting ones to
  `docs/dogfood/shared.md`.
