# Smoke Test — Azure MVP: Findings

Running log of every issue surfaced while executing
[`smoke-test-mvp-azure.md`](./smoke-test-mvp-azure.md) against a fresh Azure
subscription, plus the fix applied (code or doc).

| Session | Value |
|---|---|
| Started | 2026-04-21 |
| Operator | `gambtho` |
| Azure subscription | `d0ecd0d2-779b-4fd0-8f04-d46d07f05703` |
| Resource group | `rg-cronfoundry-p7smoke` |

---

## F1 — Runbook §3 uses parameter names that don't exist in `main.bicep`

**Severity:** blocker (deploy fails immediately)
**Type:** doc

Runbook §3 instructs operators to set `envName`, `containerImageTag`, and
`adminLogins: ["<your-github-login>"]` (array). `deploy/main.bicep` declares
`env`, `imageTag`, and `adminLogins` (comma-separated string). First
`az deployment sub create` would error on unknown parameters.

**Fix:** doc — §3 params block rewritten to use the real names per
`deploy/params.example.json`.

## F2 — Runbook §3 omits required Bicep parameters

**Severity:** blocker
**Type:** doc

`main.bicep` requires `githubAppId`, `githubAppOAuthClientId`,
`githubAppOAuthClientSecret`, `postgresAdminPassword`, and `masterKey`. None
appear in §3.

**Fix:** doc — §3 now lists every required param, explains where each value
comes from (§2 for GitHub App creds; generated for master key; operator-chosen
for Postgres password), and notes the URL-safe restriction on the Postgres
password.

## F3 — `ingressExternal` defaults to `false`; GitHub webhook needs public ingress

**Severity:** blocker
**Type:** doc

`main.bicep`'s default is `ingressExternal: false`, so the Container App gets
only an internal FQDN. GitHub's outbound webhook delivery can't reach it.

**Fix:** doc — §3 params example sets `ingressExternal: true` and warns that
the default silently breaks webhook delivery.

## F4 — Master-key generation not documented in §3

**Severity:** blocker
**Type:** doc

`masterKey` is a required param; `params.example.json` just says
`RUN_cronfoundry_admin_init_TO_GENERATE`. §3 never tells the operator how to
produce one.

**Fix:** doc — §3 adds a "Generate a master key" pre-step:
- Fast path: `openssl rand -base64 32` (32 random bytes, standard base64 —
  exactly what `internal/secretstore/envelope.go:138` produces).
- Canonical path: `make build && ./cronfoundry admin init` — prints the
  env-line form.

## F5 — No container image published to GHCR

**Severity:** blocker
**Type:** operational + doc

`release.yml` fires on `v*` tags only. Last tag is `v0.2.0-p2d` (P2); all
P3–P7 code is unreleased. `gh api /users/gambtho/packages/container/cronfoundry`
returns `404 Package not found`. Without a published image,
`ghcr.io/gambtho/cronfoundry:latest` doesn't resolve and Container App can't
start.

**Fix:** operational — tag `v0.7.0` on main to trigger `release.yml`. Runbook
gains a new §3 "Publish a container image" that documents the tag push and
the wait.

## F6 — GHCR image visibility (observed: already public for this repo)

**Severity:** potentially blocker (turned out not to be)
**Type:** doc

Hypothesis going in: first CI push creates a **private** GHCR package,
and `containerApp.bicep` has no `imagePullSecrets`, so the Container App
would 401 until the package is manually flipped to public.

Observed on live push (`v0.7.0`, release run 24757432665): the package
was public on first push — `docker manifest inspect
ghcr.io/gambtho/cronfoundry:0.7.0` returned the manifest index
anonymously with no visibility change. Likely because
`gambtho/cronfoundry` is a public source repository and GHCR inherits
that visibility.

**Fix:** doc — §3 step 2 is now a **verification** step (try the
anonymous pull; if it works you're done) with the GHCR settings URL kept
as a fallback in case the default changes on a fork, private mirror, or
future GitHub policy update.

## F7 — Container App references a KV secret `github-app-pem` that Bicep never seeded

**Severity:** blocker (pre-emptively fixed)
**Type:** code

`deploy/modules/containerApp.bicep:44` declares a Key-Vault-backed secret
referencing `${kvUrl}secrets/github-app-pem`. `deploy/modules/keyVault.bicep`
created the vault but not the secret; unclear whether Container Apps
would tolerate a missing KV-referenced secret at create time.

Never actually tested in isolation — resolution was bundled into the F14
fix. See F14 for the implementation: `keyVault.bicep` now accepts a
`@secure() githubAppPem` param and creates the secret during deploy when
non-empty.

**Fix:** code — bundled with F14.

## F8 — `main` is red from a staticcheck lint error

**Severity:** blocker (CI failure on every PR that branches from main)
**Type:** code

Commit `fd91fd6` (PR #15, "MVP follow-ups") landed on main while CI was
failing:

```
internal/webapi/oauth_test.go:273:9: S1024: should use time.Until instead
of t.Sub(time.Now()) (staticcheck)
    ttl := time.Unix(claims.Exp, 0).Sub(time.Now())
```

Noticed because PR #16 (this doc fix branch) inherited the red CI. The
release workflow doesn't run lint, so a `v*` tag push would still produce
an image — but every future branch is blocked from merging until main is
green again.

**Fix:** code — one-line change in `oauth_test.go:273` to
`time.Until(time.Unix(claims.Exp, 0))`. Landed on this branch so PR #16
also unblocks main.

## F9 — Runbook §3 wrote `v0.7.0` as an image tag; metadata-action strips the `v`

**Severity:** blocker (deploy would pull a nonexistent tag)
**Type:** doc

The initial §3 rewrite said the release would push
`ghcr.io/<owner>/cronfoundry:{v0.7.0, 0.7, latest}` and set
`"imageTag": "v0.7.0"` in the params example. In reality
`docker/metadata-action@v5` is configured with
`type=semver,pattern={{version}}`, which **strips the leading `v`** —
the actual tags pushed are `0.7.0`, `0.7`, and `latest`.

Caught when `docker manifest inspect ghcr.io/gambtho/cronfoundry:v0.7.0`
returned `manifest unknown` after a successful release run; inspecting
the run log confirmed the pushed tags were `0.7.0` / `0.7` / `latest`.
If we hadn't caught this, the Container App would fail to pull on the
first deploy.

**Fix:** doc — §3 now names the exact pushed tags (prefix-stripped) and
sets `"imageTag": "0.7.0"` in the params example.

## F10 — Runbook §2 let an operator register an OAuth App instead of a GitHub App

**Severity:** blocker (Bicep's `githubAppId` param has no analogue in an OAuth App)
**Type:** doc

§2 opened with "GitHub → Settings → Developer settings → GitHub Apps →
**New GitHub App**." Both OAuth Apps and GitHub Apps live on adjacent
pages under Developer settings, and the runbook never explained what
distinguishes them. An operator who landed on *OAuth Apps* by accident
produced an App with Client ID + Client Secret (no App ID, no private
key), then hit a dead end when the Bicep asked for `githubAppId`.

Observed on this smoke: registered an OAuth App first, got Client ID
`Ov23li…` (an OAuth App prefix; GitHub Apps use `Iv23li…`) and no
**Private keys** section on the settings page, then had to start over
with a real GitHub App.

**Fix:** doc — §2 now opens with a callout contrasting GitHub Apps vs
OAuth Apps, links directly to `/settings/apps/new`, tells the operator
to check the URL to confirm they're on the right form, and splits the
"Save" step so the private-key download and client-secret generation are
unmistakable.

## F11 — Bicep main.bicep failed to compile at `law.outputs.primarySharedKey`

**Severity:** blocker (compilation error — az never submitted the deployment)
**Type:** code

`deploy/main.bicep:79` passed `law.outputs.primarySharedKey` to the
`containerAppsEnv` module. AVM's `operational-insights/workspace:0.9.0`
does not expose `primarySharedKey` as a module output (by design — keys
are secrets). `az deployment sub create` exited with `BCP053: The type
'outputs' does not contain property 'primarySharedKey'`.

First attempted fix — `listKeys(law.outputs.resourceId, '2022-10-01')`
called from `main.bicep` — hit `BCP181: This expression is being used
in an argument of the function 'listKeys', which requires a value that
can be calculated at the start of the deployment.` Module outputs are
resolved mid-deployment, not at plan time.

**Fix:** code — moved the lookup inside `containerAppsEnv.bicep`:
declare the workspace with an `existing` resource keyed by name and
call `law.listKeys().primarySharedKey` from the rg-scoped module. Pass
only `logAnalyticsWorkspaceName` from `main.bicep`. As a side effect
the now-redundant `logAnalyticsWorkspaceId` / `logAnalyticsCustomerId`
/ `logAnalyticsSharedKey` params are gone, clearing the stale
`no-unused-params` warning on `logAnalyticsWorkspaceId`.

Committed on branch `fix/smoke-f11-bicep-listkeys` (worktree); fast-
forwarded into `fix/smoke-runbook-azure-p7` and pushed to PR #16.

## F12 — Key Vault deploy rejected `enablePurgeProtection: false`

**Severity:** blocker
**Type:** code

Second deploy attempt reached the KV creation step and failed with:

```
BadRequest: The property "enablePurgeProtection" cannot be set to
false. Enabling the purge protection for a vault is an irreversible
action.
```

`keyVault.bicep` defaults `enablePurgeProtection = false` and
`main.bicep` did not override it, so Azure received `false`. The
Microsoft-internal subscription used for this smoke
(`d0ecd0d2-779b-4fd0-8f04-d46d07f05703`) enforces purge protection via
a custom Key Vault policy — `az policy assignment list` surfaced
"Custom Azure Key Vault RBAC permission model Policy" and "Custom
Resource logs on Key Vault should be enabled Policy" on the sub. The
error message is Azure's generic "can't turn it off once on" wording
even though our vault didn't exist yet; the policy evaluator treats
the incoming `false` as a forbidden transition from the required
state.

**Fix:** code — `main.bicep`'s KV module call now sets
`enablePurgeProtection: true` with an inline comment explaining the
tradeoff (soft-deleted vault lingers for `softDeleteRetentionDays`
= 7, so re-deploys with the same env suffix need to wait the window
out or use a different `env`). The module default stays `false` so
non-enforcing subs aren't affected.

## F13 — Postgres Flexible Server offer-restricted in multiple regions

**Severity:** blocker
**Type:** operational + doc

Second deploy failed with:

```
LocationIsOfferRestricted: Subscriptions are restricted from
provisioning in location 'eastus'. Try again in a different location.
```

Not a CronFoundry bug — the subscription enforces Postgres Flexible
Server offer restrictions per-region. Initial spot-check with
`az postgres flexible-server list-skus` returned SKUs for every
candidate region, but `list-skus` only reports SKU catalog presence,
not whether a given subscription can actually provision. The real
check is an actual create attempt.

First fix tried `eastus2` — deploy failed with the same error. Switched
to parallel probes: launched `az postgres flexible-server create
--no-wait` in `westus2`, `westus3`, `centralus`, `northeurope`,
`westeurope`, `swedencentral`. All returned exit 0, but 45 s later none
of the servers existed — `--no-wait` swallows the downstream async
provisioning failure (`LocationIsOfferRestricted` fires after
submission, not at submit time, so CLI exit 0 is misleading).

Synchronous probe in `swedencentral` succeeded (operator flagged it as
a known-good region for this sub) — server reached `Ready` state in
~2 min. Final `params.p7smoke.json` uses `swedencentral`.

**Fix:** operational — `params.p7smoke.json` location set to
`swedencentral`. Two full RG delete + redeploy cycles consumed on
this finding (eastus → eastus2 → swedencentral).

Runbook §1 / §3 stays region-agnostic. Added a callout in the runbook
that if an operator's sub is offer-restricted on Postgres, the reliable
probe is a **sync** `az postgres flexible-server create` (not
`--no-wait`), because the restriction fires post-submission.

## F14 — Key Vault name collision with soft-deleted vault across region pivots

**Severity:** blocker
**Type:** operational + doc + code (pre-emptive F7 fix bundled)

Third deploy (in `swedencentral`, post-F13 fix) failed at the KV step:

```
VaultAlreadyExists: The vault name 'cf-kv-p7smoke' is already in use.
If the vault is in a recoverable state then the vault will need to be
purged before reusing the name.
```

`az keyvault list-deleted` confirmed `cf-kv-p7smoke` soft-deleted in
`eastus2` from the second attempt, with `purgeProtectionEnabled: true`
(from the F12 fix) and `scheduledPurgeDate` 7 days out. Purge is
blocked until then. Postgres made it in though — all other resources
provisioned in swedencentral, **only** the KV collided.

Root cause chain: F12 enabled purge protection on the KV to satisfy the
sub policy; F13 forced an RG delete to migrate regions; F13's RG delete
soft-deleted the eastus2 KV; the soft-deleted KV now owns its globally-
unique name for 7 days.

**Fix:** operational — `params.p7smoke.json` `env` bumped to `p7smoke2`
so every resource gets a new name (`cf-kv-p7smoke2`, `cf-pg-p7smoke2`,
etc.). Runbook `env` callout in §4b updated to spell out the 7-day
name-lock tradeoff.

Opportunistically bundled a pre-emptive fix for F7 (previously pending):
`keyVault.bicep` now accepts a `@secure()` `githubAppPem` param and
creates a `github-app-pem` secret during deploy when the param is
non-empty. `main.bicep` surfaces the same param and passes it through.
Consequences:
- The serve Container App's KV secret reference resolves at creation
  time — no post-deploy upload step needed for the common path.
- Runbook §4b picks up a new param slot with a `python3` snippet that
  inlines the pem contents; §4d is reframed as an "only if you passed
  empty" fallback.
- Module default is `''` (empty) with an `if (!empty(...))` guard, so
  existing callers don't break.

F7 retroactively marked resolved via this bundle.

## F15 — `serve` treats `CRONFOUNDRY_GITHUB_APP_PEM` strictly as a file path

**Severity:** blocker (Container App CrashLoopBackOff after F14 deploy succeeded)
**Type:** code

Fourth deploy succeeded — all Azure resources including the pre-seeded
`github-app-pem` KV secret landed. But the serve Container App
immediately started crash-looping (restartCount=11 inside 30 min,
`runningState: ActivationFailed`, `health: Unhealthy`). Log Analytics
query of `ContainerAppConsoleLogs_CL` showed:

```
error: read PEM: open -----BEGIN RSA PRIVATE KEY-----
<... full PEM body ...>
-----END RSA PRIVATE KEY-----
: no such file or directory
```

Root cause: `cmd/cronfoundry/serve.go:72-95` did
`os.ReadFile(os.Getenv("CRONFOUNDRY_GITHUB_APP_PEM"))` — i.e. treated
the env var strictly as a filesystem path. Same pattern in
`cmd/cronfoundry/admin_triggersync.go:48-55`. Local/docker-compose
deploys mount a `.pem` file and pass its path, so the pattern worked
there. In Azure Container Apps the Key Vault secret is mapped
**inline** into the env var (secretRef at `containerApp.bicep:63`),
so the process gets the multi-line PEM text and tries to open it as
a path.

**Fix:** code — new `github.ReadPEM(value string) ([]byte, error)` in
`internal/github/pem.go` auto-detects inline vs. path by checking for
a leading `-----BEGIN` marker after trimming whitespace; falls back to
`os.ReadFile`. `serve.go` and `admin_triggersync.go` swapped to use it.
Preserves the local dev contract (env var = path) and unblocks the
Azure deploy (env var = inline content). Unit test in
`internal/github/pem_test.go` covers both modes plus leading-whitespace
and missing-file cases.

Requires a new release tag so the Container App can pull an image with
the fix. Tagging `v0.7.1` off this branch; release workflow runs on
any `v*` tag regardless of base branch. After the image builds,
`params.p7smoke.json` `imageTag` bumps to `0.7.1` and a second
`az deployment sub create` is incremental — only the Container App
revision changes.

## F16 — Postgres Flexible Server has no firewall rules; Container App can't connect

**Severity:** blocker (Container App crash-loops with `dial error: timeout`)
**Type:** code

Fifth deploy (v0.7.1 with F15 inline-PEM fix) resolved the PEM crash,
but the serve Container App immediately started failing with:

```
failed to connect to `user=cfadmin database=cronfoundry`:
20.91.204.171:5432 (cf-pg-p7smoke2.postgres.database.azure.com):
dial error: timeout: context deadline exceeded
```

Root cause: `deploy/modules/postgres.bicep` creates the Flexible Server
with `publicNetworkAccess: Enabled` (the default when no VNet params are
passed) but defines **zero firewall rules**. Azure's default-deny means
all inbound connections are blocked — including from Container Apps in
the same region.

**Fix:** code — added an
`AllowAllAzureServicesAndResourcesWithinAzureIps` firewall rule
(`0.0.0.0` → `0.0.0.0`) conditioned on `!usePrivateNetwork`. When VNet
integration is used, the rule is skipped (traffic flows through the
delegated subnet instead). Forced a new Container App revision after
the incremental redeploy since failed revisions don't auto-recover.

## F17 — Postgres `uuid-ossp` and `citext` extensions not allow-listed on Azure

**Severity:** blocker (migrations fail on `admin init`)
**Type:** code + doc

After F16 unblocked Postgres connectivity, running `cronfoundry admin init`
against the Azure Postgres failed:

```
ERROR: extension "uuid-ossp" is not allow-listed for users in
Azure Database for PostgreSQL (SQLSTATE 0A000)
```

After allow-listing `UUID-OSSP`, the next migration hit the same error for
`citext`. Azure Postgres Flexible Server requires extensions to be
explicitly enabled via the `azure.extensions` server parameter before
`CREATE EXTENSION` can succeed.

**Fix:** code — `postgres.bicep` now sets the `azure.extensions` server
configuration to `UUID-OSSP,CITEXT` via a
`Microsoft.DBforPostgreSQL/flexibleServers/configurations` resource.
Runbook §4c gains a note that `admin init` must be run after the first
deploy to migrate the schema and seed the org — `serve` does not
auto-migrate.

## F18 — Key Vault role too restrictive for secret writes

**Severity:** blocker (secret creation via web UI returns 500)
**Type:** code

Creating a secret via `POST /api/secrets` returned
`{"error":"failed to create secret","code":"internal"}`. The serve
Container App's managed identity had **Key Vault Secrets User**
(`4633458b-17de-408a-b874-0445c86b69e6`) — read-only. The Azure KV
secret store needs write access to create/rotate secrets.

**Fix:** code — `keyVault.bicep` role upgraded to **Key Vault Secrets
Officer** (`b86a8fe4-44ce-4948-aee5-eccb2c155cd7`).

## F19 — Serve identity lacks permission to start runner job

**Severity:** blocker (scheduler tick always 403)
**Type:** code

Every 30 s the scheduler tried to dispatch the pending run and got:

```
AuthorizationFailed: … does not have authorization to perform action
'Microsoft.App/jobs/start/action' over scope …/jobs/cf-runner-p7smoke2
```

The Bicep had no role assignment granting the serve identity permission
to start the Container Apps Job.

**Fix:** code — added `deploy/modules/roleAssignment.bicep` (generic
RBAC module) and a `serveJobStartRole` call in `main.bicep` granting
**Contributor** on the resource group to the serve principal. Contributor
includes `Microsoft.App/jobs/start/action`.

## F20 — Job execution template missing container name

**Severity:** blocker (dispatch returns 400 even after RBAC fix)
**Type:** code

After F19 RBAC propagated, the dispatch flipped from 403 to 400:

```
ContainerAppInvalidDNS1123LabelName: Property 'containers.name' has
an invalid value '<null>'.
```

`internal/cloud/azure/armclient_real.go:toARMTemplate()` creates a
`JobExecutionContainer` without setting `Name`. Azure requires a valid
DNS-1123 label.

**Fix:** code — set `Name: &"runner"` on the container. Requires
v0.7.2 image tag + redeploy.

## F21 — Job execution template missing container image

**Severity:** blocker (dispatch returns 400 after F20 fix)
**Type:** code + infra

After deploying v0.7.2 (F20 fix), dispatch returned:

```
ContainerAppImageRequired: Container with name 'runner' must have an
'Image' property specified.
```

When overriding the Container Apps Job execution template, Azure
requires re-specifying the `Image` even though the job definition
already sets it. `toARMTemplate()` did not populate `Image`.

**Fix:** code — added `ContainerImage` to `JobExecutionTemplate`,
set `Image` on `JobExecutionContainer` in `toARMTemplate()`. Added
`AZURE_CAE_JOB_IMAGE` env var to `containerApp.bicep` and read in
`serve.go`. Requires v0.7.3 image tag + redeploy.
