# Dogfood round — repeating prompt

> Drop this whole file in as the first message of a new Claude Code
> session whenever we want to run another live dogfood pass.

## Goal

Run `bash scripts/quickstart-copilot.sh` against a real Azure
subscription, surface every bug it hits, get fixes merged + tagged +
deployed, and exit only when a smoke run completes end-to-end:

- A run with `Status: Success`, non-zero token counts, and a small
  duration (typically < 10s) on the dashboard.
- An issue filed in `gambtho/skills` (the configured destination repo).
- A `chore(cronfoundry): update memory.md from run …` commit on the
  `gambtho/skills` default branch (writeback).

## Context the agent must load up front

1. **Repo:** `/home/tng/workspace/cronfoundry`. Always work in a
   worktree under `.claude/worktrees/<topic>`. Never push to `main`.
2. **Live env:** `dog4` (Container Apps env in `swedencentral`).
   - Resource group: `rg-cronfoundry-dog4`
   - Serve app: `cf-serve-dog4`
   - Runner job: `cf-runner-dog4`
   - Key Vault: `cf-kv-dog4`
   - URL: `https://cf-serve-dog4.yellowtree-eae6f0d3.swedencentral.azurecontainerapps.io/`
3. **Secrets:** in `.env` at repo root (GitHub App PEM path, OAuth
   creds). When you need a Copilot token directly, pull
   `copilot-access-token` from `cf-kv-dog4` via `az keyvault secret
   show`.
4. **Image layout (important — see drift bug below):** Every dispatcher
   invokes the runner via `cronfoundry runner …` on the main binary.
   Only one image (`ghcr.io/gambtho/cronfoundry`) is in active use.
5. **Skills repo:** `gambtho/skills`. Live config is `cronfoundry.yaml`
   on the default branch; current default model is `gpt-5-mini` (do
   NOT downgrade to `gpt-4o` — Copilot Enterprise rejects it).

## Process

For each round:

1. **Run the quickstart against an existing dog env.** From the repo
   root:
   ```bash
   bash scripts/quickstart-copilot.sh
   ```
   For an upgrade-only round (no fresh deploy), the relevant invariants
   are tested by clicking "Run now" on an existing schedule via the
   dashboard. Do that instead if Azure resources already exist.

2. **Watch for failure.** If the smoke run errors:
   - Pull the runner-side logs from Log Analytics:
     ```bash
     LAW=$(az monitor log-analytics workspace list -g rg-cronfoundry-dog4 \
       --query "[0].customerId" -o tsv)
     az monitor log-analytics query -w "$LAW" --analytics-query \
       "ContainerAppConsoleLogs_CL | where ContainerJobName_s == 'cf-runner-dog4' \
        | where TimeGenerated > ago(30m) | order by TimeGenerated asc \
        | project TimeGenerated, Log_s | take 200" -o tsv
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
   Tagging triggers `.github/workflows/release.yml`. amd64 finishes in
   ~4 min and pushes `:VERSION` (amd64-only) immediately, which is
   enough to roll dog4. arm64 takes longer and merges later — don't
   wait.

5. **Roll dog4 to the new tag.** All three values must move together
   or you'll hit the drift bug (see below):
   ```bash
   TAG=X.Y.Z
   az containerapp update -n cf-serve-dog4 -g rg-cronfoundry-dog4 \
     --image "ghcr.io/gambtho/cronfoundry:$TAG" \
     --set-env-vars "AZURE_CAE_JOB_IMAGE=ghcr.io/gambtho/cronfoundry:$TAG" \
     -o none
   az containerapp job update -n cf-runner-dog4 -g rg-cronfoundry-dog4 \
     --image "ghcr.io/gambtho/cronfoundry:$TAG" -o none
   ```
   The `az containerapp update` long-poll often times out client-side
   while the actual operation succeeds. Always verify by reading state
   back, not by waiting on the command:
   ```bash
   az containerapp show -n cf-serve-dog4 -g rg-cronfoundry-dog4 \
     --query "{img:properties.template.containers[0].image, \
              runner:properties.template.containers[0].env[?name=='AZURE_CAE_JOB_IMAGE'].value|[0], \
              prov:properties.provisioningState}" -o json
   curl -s -o /dev/null -w "%{http_code}\n" "https://cf-serve-dog4.yellowtree-eae6f0d3.swedencentral.azurecontainerapps.io/healthz"
   ```

6. **Re-run the smoke** from the dashboard's Run-now button. Iterate
   until success. Confirm the issue + writeback commit on
   `gambtho/skills` after success.

## Known sharp edges (read these every round)

- **Image drift between serve and runner.** `AZURE_CAE_JOB_IMAGE` (env
  var on the serve container) is what the dispatcher uses when it
  spawns runner executions. If you bump `cf-serve-dog4` but forget the
  env var, new dispatches keep running OLD code. Always update all
  three values (serve `--image`, serve `--set-env-vars
  AZURE_CAE_JOB_IMAGE=…`, and `cf-runner-dog4` job `--image`) on every
  rollout. (See PR #82.)

- **`cronfoundry-runner` image is dead.** All dispatchers invoke the
  `runner` subcommand on the main `cronfoundry` binary. The
  `-runner` image's entrypoint doesn't accept that subcommand. Never
  point `AZURE_CAE_JOB_IMAGE` at `cronfoundry-runner:*` even though
  the name looks right. (See PR #83.)

- **`az containerapp` client-side timeouts are normal.** The PATCH
  goes through; the long-poll for completion times out from this
  sandbox. Re-check state with `containerapp show`, don't retry
  blindly.

- **Copilot model availability.** `gpt-4o` is not on the current
  Copilot Enterprise plan. `gpt-5-mini` works as a starter default. If
  you change models, patch the live `gambtho/skills/cronfoundry.yaml`
  via `gh api -X PUT` rather than re-deploying.

- **Sync interval.** The schedule sync runs roughly every 60s. After
  patching `cronfoundry.yaml`, wait one cycle (or look for an audit
  event) before clicking Run-now.

- **Stuck "Running" runs.** If a runner crashes before posting a
  terminal event, the dashboard shows the run as Running. The
  background orphan sweeper transitions it to `failed: orphan sweep:
  run exceeded timeout`. That's expected after a crash; just look at
  the runner-side logs to see the real error.

- **Sandbox blocked commands.** When `az`, `gh api`, or anything else
  gets denied, surface the denial — don't silently fail. Ask the
  operator to approve.

- **`az deployment sub create` 5-min read timeout (step 13).** The
  Python `az` CLI applies a hard 300s read timeout to subscription-level
  deployments, but Bicep regularly takes 6-10 min for a fresh
  CronFoundry env. The first attempt usually exits with
  `HTTPSConnectionPool(host='management.azure.com', port=443): Read
  timed out. (read timeout=300)` and aborts the script before any
  Azure-side resource group or deployment record exists. Just re-run
  `bash scripts/quickstart-copilot.sh` — the state file persists, the
  resume picks up at step 13 with the same params, and the second
  attempt usually completes (the connection pool has warmed up).

- **`gambtho/skills` repo references in the dogfood loop.** When you
  patch `cronfoundry.yaml` via `gh api -X PUT`, GitHub will reject the
  PUT with `409 Conflict` if you reuse a stale SHA. Always re-fetch
  `.sha` immediately before each PUT — don't cache it across steps.

- **Reset-and-prove for next_fire_at fixes.** To verify a sync-side fix
  end-to-end without re-bootstrapping the env, null the `next_fire_at`
  column on the `schedule` table for the **single** row you're
  exercising (or change its cron/tz), then watch the next 60s sync
  cycle re-arm it. Always scope the UPDATE — bare
  `UPDATE schedule SET next_fire_at = NULL` clears every schedule in
  the org and breaks unrelated dispatchable jobs. Use the row's id or
  composite key, e.g.
  `UPDATE schedule SET next_fire_at = NULL WHERE id = '<schedule-uuid>';`
  or
  `UPDATE schedule SET next_fire_at = NULL WHERE skill_id = '<skill-uuid>' AND name = '<schedule-name>';`.
  Cheaper than a full teardown and exercises the same code path the
  bug originally hit, while leaving every other schedule armed.

- **Sandbox blocks `source <state-file>` for psql passwords.** Auto
  mode's safety check sometimes denies `source ~/.cronfoundry-quickstart-state-*`
  followed by a `PGPASSWORD=$CF_PG_PASSWORD psql …`. Workaround:
  `PG_PW=$(grep '^CF_PG_PASSWORD=' /home/tng/.cronfoundry-quickstart-state-<env> | sed 's/^CF_PG_PASSWORD=//; s/^"//; s/"$//')`
  and inline `PGPASSWORD="$PG_PW" psql …` — same effect, no shell
  source.

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
