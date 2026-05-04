# Fly.io smoke test process — design

## Goal

Give CronFoundry an end-to-end smoke flow on Fly.io that mirrors the
Azure `dogfood-round.md` loop: a customer can run one script to stand
up a working deployment, and an internal operator can drive the same
script under a repeating prompt to surface bugs round after round.

Success = a dashboard run with `Status: Success`, non-zero token
counts, an issue filed in the configured skills repo, and a writeback
commit on that repo's default branch.

## Artifacts

Three deliverables, each with one clear job:

1. **`scripts/fly-quickstart.sh`** — customer-facing provisioning &
   deploy. Idempotent. Reads `.env`. Long-lived by default;
   `--fresh` destroys everything and re-provisions.
2. **`scripts/fly-smoke-assert.sh`** — internal-leaning verification.
   Triggers a Run-now via the API, polls the run to terminal state,
   asserts tokens > 0, then asserts issue + writeback on the
   configured skills repo via `gh`.
3. **`docs/superpowers/prompts/fly-dogfood-round.md`** — repeating
   prompt drop-in, same shape as `dogfood-round.md`. Drives the two
   scripts and lists fly-specific sharp edges.

The split keeps `fly-quickstart.sh` clean enough to ship to customers
(it makes no assumptions about who owns the skills repo) while the
assert script and prompt carry the dogfood-only concerns.

## `scripts/fly-quickstart.sh`

### Inputs

Reads `.env` at repo root. Required keys:

- `FLY_API_APP` (default `cronfoundry-api`)
- `FLY_RUNNER_APP` (default `cronfoundry-runner`)
- `FLY_REGION` (default `iad`)
- `IMAGE` (default `ghcr.io/gambtho/cronfoundry:latest`; `--image` flag overrides)
- `CRONFOUNDRY_GITHUB_APP_ID`
- `CRONFOUNDRY_GITHUB_APP_PEM_PATH`
- `CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID`
- `CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET`
- `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`
- `CRONFOUNDRY_ADMIN_LOGINS`

Generated and written back into `.env` if absent (so reruns are stable):

- `CRONFOUNDRY_MASTER_KEY` — `openssl rand -hex 32`
- `CRONFOUNDRY_RUNNER_API_KEY` — `openssl rand -hex 32`

Flags:

- `--image <ref>` — override `IMAGE`.
- `--fresh` — destroy api app, runner app, and Postgres cluster, then re-provision.
- `--non-interactive` — fail rather than prompt for missing keys.

### Steps

1. **Preflight.** `flyctl auth whoami`; verify required `.env` keys
   present; resolve image; ensure `gh` is authenticated (warning, not
   fatal — only the assert script needs it).
2. **`--fresh` (if set).** `flyctl apps destroy --yes` for api +
   runner; `flyctl postgres destroy --yes cronfoundry-db` if it
   exists. Strip generated keys from `.env` so step 5 regenerates.
3. **Apps.** `flyctl apps create $FLY_API_APP` and `$FLY_RUNNER_APP`,
   skip if `flyctl apps list` already shows them.
4. **Postgres.** If `flyctl postgres list` lacks `cronfoundry-db`,
   create it. If api app's secrets don't already include
   `DATABASE_URL`, run `flyctl postgres attach`.
5. **Secret generation.** Generate `CRONFOUNDRY_MASTER_KEY` and
   `CRONFOUNDRY_RUNNER_API_KEY` if missing; append to `.env`.
6. **Set api secrets.** One `flyctl secrets set` call (rolling
   restart fires once, not per key) for all API-side secrets,
   including `FLY_RUNNER_APP`, `FLY_RUNNER_IMAGE=$IMAGE`, and
   `FLY_API_TOKEN=$(flyctl auth token)`.
7. **Set runner secret.** `CRONFOUNDRY_RUNNER_API_KEY` only.
8. **Deploy api** and **deploy runner** (`--no-ha`) with the same
   `$IMAGE`.
9. **Healthcheck.** Poll `https://$FLY_API_APP.fly.dev/healthz` until
   200 or 60s timeout.
10. **Report.** Print URL, image tag, and a one-line `next:
    scripts/fly-smoke-assert.sh` hint.

### Idempotency

Every step is "create-or-skip" or "set" (which fly treats as
upsert). Re-running is safe and rolls forward to the requested image.

## `scripts/fly-smoke-assert.sh`

### Inputs

- Same `.env` as fly-quickstart.
- Additional: `CRONFOUNDRY_SKILLS_REPO` (e.g. `gambtho/skills`),
  `CRONFOUNDRY_SCHEDULE_ID` (the schedule to Run-now).

### Steps

1. POST `/api/schedules/$ID/run-now` against the api hostname using
   the admin OAuth path or a service token (whichever the dashboard
   uses; resolved during plan).
2. Poll the run via `/api/runs/$RUN_ID` until terminal state, 5m
   timeout. Surface `flyctl logs --app $FLY_RUNNER_APP` if it goes
   to `failed` or stays `running` past timeout.
3. Assert `status == "success"`, `tokens.input > 0`, `tokens.output > 0`.
4. `gh issue list -R $CRONFOUNDRY_SKILLS_REPO --search "run $RUN_ID"`
   — assert one match.
5. `gh api repos/$CRONFOUNDRY_SKILLS_REPO/commits` — assert the most
   recent commit on default branch has a
   `chore(cronfoundry): update memory.md from run $RUN_ID` subject.
6. Print run id, tokens, duration, issue URL, commit SHA.

Exit non-zero on any assertion failure so the dogfood prompt loop can
detect it cleanly.

## `docs/superpowers/prompts/fly-dogfood-round.md`

Same section structure as `dogfood-round.md`:

- **Goal** — fly equivalent.
- **Context** — `.env`, `FLY_*` apps, image source, skills repo.
- **Process** — quickstart → assert → on failure pull `flyctl logs
  --app $FLY_RUNNER_APP` → fix in a worktree → PR → tag → re-run.
- **Sharp edges** — listed below.
- **Auto mode** + **Output expected** — copied verbatim.

### Sharp edges to pre-load

- **Image drift, fly edition.** Api app holds `FLY_RUNNER_IMAGE`
  secret; runner app's deployed image must match. Bumping just one
  leaves dispatchers calling old runner code. fly-quickstart.sh
  always sets both together — only matters if you `flyctl deploy`
  one app by hand.
- **`cronfoundry-runner` image is dead** — same as Azure: dispatchers
  invoke `cronfoundry runner …` on the main binary; never point
  `FLY_RUNNER_IMAGE` at a `-runner` tag.
- **`flyctl secrets set` triggers a rolling restart.** Batch in one
  call. Re-running fly-quickstart is fine but each unbatched edit
  costs a restart.
- **Postgres attach is one-shot.** Skip on `DATABASE_URL` already
  present, or attach fails with "already attached".
- **`gpt-4o` not on Copilot Enterprise** — same as Azure; keep
  `gpt-5-mini` default; patch `cronfoundry.yaml` via `gh api -X PUT`
  to change models without redeploy.
- **Sync interval ~60s** — same as Azure.
- **Stuck "Running" / orphan sweeper** — same as Azure.
- **`.env` rewrite.** fly-quickstart appends generated
  `CRONFOUNDRY_MASTER_KEY` and `CRONFOUNDRY_RUNNER_API_KEY` to `.env`
  on first run. If `.env` is shared across operators, generate keys
  once and commit them out-of-band.
- **`flyctl apps destroy` is irreversible** — `--fresh` will wipe
  Postgres data. The prompt warns explicitly.

## Out of scope

- Multi-region deploys (single region per fly-quickstart run).
- Custom domains / TLS provisioning.
- Migration tooling between Azure dog4 and fly envs.
- CI integration of fly-smoke-assert.sh (deferred until customer
  feedback shapes the script).
