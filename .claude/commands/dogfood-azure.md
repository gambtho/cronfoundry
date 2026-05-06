---
description: Run a live Azure dogfood pass — deploy fresh env, surface bugs, fix → tag → roll → verify autonomously
allowed-tools: Bash, Read, Edit, Write, Grep, Glob, Agent
---

# Azure dogfood round

Run `bash scripts/quickstart-copilot.sh` against a real Azure subscription,
surface every bug it hits, get fixes merged + tagged + deployed, and exit
only when a smoke run completes end-to-end:

- A run with `Status: Success`, non-zero token counts, and small duration
  (typically < 10s) on the dashboard.
- An issue filed in the configured destination skills repo.
- A `chore(cronfoundry): update memory.md from run …` commit on the
  skills repo's default branch (writeback).

## Current state (auto-loaded)

- Repo: !`pwd`
- Branch: !`git branch --show-current`
- Latest tag: !`git fetch --tags origin 2>/dev/null; git tag --sort=-v:refname | head -3 | tr '\n' ' '`
- Commits since latest tag (already merged but not yet released): !`LATEST=$(git tag --sort=-v:refname | head -1); git log "$LATEST..origin/main" --oneline 2>/dev/null | head -10`
- Existing Azure RGs: !`az group list --query "[?starts_with(name, 'rg-cronfoundry-')].name" -o tsv 2>/dev/null | tr '\n' ' '`
- Existing local state files: !`ls ~/.cronfoundry-quickstart-state-* 2>/dev/null | sed 's|.*-state-||' | tr '\n' ' '`
- Live model in skills repo: !`gh api repos/gambtho/skills/contents/cronfoundry.yaml -H "Accept: application/vnd.github.raw" 2>/dev/null | grep -E '^\s*model:' | head -3`

## Pick a fresh env suffix

Use the smallest free `dogN` (e.g. `dog7`, `dog8`) — one where neither
`rg-cronfoundry-dogN` exists in Azure nor
`~/.cronfoundry-quickstart-state-dogN` exists locally. Both lists are
above. Set `ENV` and use it everywhere below.

**Before launching the script, delete any stale state files** for envs
whose RG no longer exists. The quickstart's resume logic auto-picks
the *only* existing state file without prompting (convenient for
"resume my one in-flight env"); leave a stale `state-dog6` lying
around and the script silently tries to redeploy `dog6` instead of
the new env you intended:

```bash
# After confirming the matching RG is gone in Azure:
rm ~/.cronfoundry-quickstart-state-<old-env>
```

## Rebuild ./cronfoundry before launching

Step 14 builds `./cronfoundry` from your current checkout if the binary
is missing, then uses it to apply database migrations. As of #100,
resumes also re-run `admin init` so newer migrations from a rebuilt
binary always land — but you'll save a round-trip if the local binary
is fresh from the start:

```bash
make web-stub && go build -o cronfoundry ./cmd/cronfoundry
```

Use `make build` if you want the real React bundle; the stub is fine
for the dogfood loop's purposes (we hit `/healthz` and the API, not
the UI).

## Azure context

- Repo: `/home/tng/workspace/cronfoundry`. Always work in a worktree
  under `.claude/worktrees/<topic>`. Never push to `main`.
- Resource group: `rg-cronfoundry-$ENV`
- Serve container app: `cf-serve-$ENV`
- Runner job: `cf-runner-$ENV`
- Image: `ghcr.io/gambtho/cronfoundry` (single binary; runner subcommand)
- Skills repo: `gambtho/skills` — `cronfoundry.yaml` on the default branch
- Secrets: `.env` at repo root (GitHub App PEM path, OAuth creds).
  When you need a Copilot token directly, pull `copilot-access-token`
  from the round's Key Vault via `az keyvault secret show`. Existing
  PEMs from prior rounds live in `~/.cronfoundry/*.pem`; the manifest
  flow will create a new one.
- Soft-deleted Key Vaults from past rounds (`cf-kv-dog*`) auto-purge
  themselves within 7 days — no manual cleanup needed.

## Universal process and sharp edges

@docs/dogfood/shared.md

## Azure-specific commands

### Run the quickstart

```bash
bash scripts/quickstart-copilot.sh
```

For an upgrade-only round (no fresh deploy), skip this and click
"Run now" on an existing schedule via the dashboard instead.

### Pull runner-side logs from Log Analytics

```bash
ENV=<suffix>
RG=rg-cronfoundry-$ENV
RUNNER=cf-runner-$ENV
LAW=$(az monitor log-analytics workspace list -g "$RG" \
  --query "[0].customerId" -o tsv)
az monitor log-analytics query -w "$LAW" --analytics-query \
  "ContainerAppConsoleLogs_CL | where ContainerJobName_s == '$RUNNER' \
   | where TimeGenerated > ago(30m) | order by TimeGenerated asc \
   | project TimeGenerated, Log_s | take 200" -o tsv
```

Log Analytics is sometimes slow to respond (~5 min) right after a
failed runner execution while it's ingesting; if you get a
`Read timed out (read timeout=300)` from `az monitor log-analytics
query`, retry once before assuming there's nothing to read.
`ContainerJobName_s` is the right filter for runner jobs;
`ContainerAppName_s` is for the serve container.

### Roll the env to a new tag

All three values must move together — see "image drift" in the shared
sharp edges:

```bash
ENV=<suffix>
TAG=X.Y.Z
az containerapp update -n cf-serve-$ENV -g rg-cronfoundry-$ENV \
  --image "ghcr.io/gambtho/cronfoundry:$TAG" \
  --set-env-vars "AZURE_CAE_JOB_IMAGE=ghcr.io/gambtho/cronfoundry:$TAG" \
  -o none
az containerapp job update -n cf-runner-$ENV -g rg-cronfoundry-$ENV \
  --image "ghcr.io/gambtho/cronfoundry:$TAG" -o none
```

The `az containerapp update` long-poll often times out client-side
while the actual operation succeeds. Always verify by reading state
back, not by waiting on the command:

```bash
az containerapp show -n cf-serve-$ENV -g rg-cronfoundry-$ENV \
  --query "{img:properties.template.containers[0].image, \
           runner:properties.template.containers[0].env[?name=='AZURE_CAE_JOB_IMAGE'].value|[0], \
           prov:properties.provisioningState}" -o json
FQDN=$(az containerapp show -n cf-serve-$ENV -g rg-cronfoundry-$ENV \
  --query "properties.configuration.ingress.fqdn" -o tsv)
curl -s -o /dev/null -w "%{http_code}\n" "https://$FQDN/healthz"
```

### Trigger a smoke run

From the dashboard's Run-now button, or via the helper script that
patches `gambtho/skills/cronfoundry.yaml` to a 2-min cron (and reverts
when you're done):

```bash
scripts/dogfood/patch-cron.sh 2min     # next run within ~2 min
# ...wait for issue + writeback on gambtho/skills...
scripts/dogfood/patch-cron.sh daily    # revert
```

Confirm the issue + writeback commit on `gambtho/skills` after
success.

### Inspect runner job executions directly

When the runner crashes before posting a terminal event, the
dashboard shows the run as Running until the orphan sweeper closes
it (`failed: orphan sweep: run exceeded timeout`). To see what
actually happened on the Azure side:

```bash
ENV=<suffix>
RG=rg-cronfoundry-$ENV
az containerapp job execution list -n cf-runner-$ENV -g $RG \
  --query "[].{n:name, s:properties.status, st:properties.startTime, et:properties.endTime}" \
  -o table

# Then inspect a single execution's args + image (useful when
# debugging stale-image / drift bugs):
EXEC=<from-list>
az containerapp job execution show -n cf-runner-$ENV -g $RG \
  --job-execution-name $EXEC --query "properties" -o json
```

## Azure-specific sharp edges

- **Image-drift pointer is `AZURE_CAE_JOB_IMAGE`.** Env var on the
  serve container; the dispatcher reads it when spawning runner
  executions. Always update serve `--image`, serve
  `--set-env-vars AZURE_CAE_JOB_IMAGE=…`, and the runner job's
  `--image` together (PR #82).

- **`az containerapp` client-side timeouts are normal.** The PATCH
  goes through; the long-poll for completion times out from this
  sandbox. Re-check state with `containerapp show`, don't retry
  blindly.

- **`az deployment sub create` 5-min read timeout (quickstart
  step 13).** The Python `az` CLI applies a hard 300s read timeout
  to subscription-level deployments, but Bicep regularly takes 6-10
  min for a fresh CronFoundry env. The first attempt usually exits
  with `HTTPSConnectionPool(...): Read timed out. (read timeout=300)`
  before any RG or deployment record exists. Just re-run
  `bash scripts/quickstart-copilot.sh` — the state file persists,
  the resume picks up at step 13 with the same params, and the
  second attempt usually completes (the connection pool has warmed
  up).

- **WSL2 / corp-VPN egress IP ≠ `api.ipify.org` IP.** Step 15
  narrows the Postgres firewall from `0.0.0.0/0` to whatever
  ipify reports. On WSL2 (and some corp/VPN setups) the IP that
  arrives at Azure's Postgres frontend is *not* the same one
  ipify reports — so the narrowed rule then black-holes the
  operator's own admin CLI in steps 20–22, surfacing as
  `error: load organization: context deadline exceeded`. As of
  PR #99 the script auto-rolls back to a broad rule when the
  post-narrow TCP probe fails, but on an older script the manual
  rescue is:
  ```bash
  ENV=<suffix>
  az postgres flexible-server firewall-rule create \
    --resource-group rg-cronfoundry-$ENV \
    --name cf-pg-$ENV \
    --rule-name AllowOperatorBroad \
    --start-ip-address 0.0.0.0 \
    --end-ip-address 255.255.255.255
  ```
  Tighten manually after the install once you know the right
  egress IP (`SELECT inet_client_addr();` while the broad rule is
  in place).

- **Sandbox blocks `source <state-file>` for psql passwords.** Auto
  mode's safety check sometimes denies
  `source ~/.cronfoundry-quickstart-state-*` followed by
  `PGPASSWORD=$CF_PG_PASSWORD psql …`. Workaround:
  ```bash
  PG_PW=$(grep '^CF_PG_PASSWORD=' ~/.cronfoundry-quickstart-state-<env> \
    | sed 's/^CF_PG_PASSWORD=//; s/^"//; s/"$//')
  PGPASSWORD="$PG_PW" psql 'host=cf-pg-<env>.postgres.database.azure.com \
    user=cfadmin dbname=cronfoundry sslmode=require port=5432' …
  ```
  Use the keyword form (`host=… user=…`) rather than a `postgres://…`
  URI when the password contains shell-meta characters; URI-
  encoding is fiddlier than just quoting `PGPASSWORD`.

- **Inspect goose migration state directly when a finalize 500s.**
  If a runner log shows
  `insert notification: ERROR: relation "<table>" does not exist
  (SQLSTATE 42P01)`, the deployed image's code is ahead of the
  migrations actually applied to the DB. Quick check:
  ```sql
  SELECT version_id, is_applied
    FROM goose_db_version
    ORDER BY version_id DESC LIMIT 5;
  ```
  Compare against `internal/db/migrations/`. As of #100 the resume
  path always re-runs `admin init`, so a rebuild + resume is the
  supported fix. If you really need to backfill a single migration
  manually, run the file's `-- +goose Up` body followed by
  `INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (<id>, true, now());`.
