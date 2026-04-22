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

## F7 (pending) — Container App references a KV secret `github-app-pem` that Bicep never seeds

**Severity:** unknown (likely deploy-blocker)
**Type:** code or doc

`deploy/modules/containerApp.bicep:44` declares a Key-Vault-backed secret
referencing `${kvUrl}secrets/github-app-pem`. `deploy/modules/keyVault.bicep`
creates the vault but no secret. Unclear whether Container Apps tolerates a
missing KV-referenced secret at create time.

**Fix:** pending live deploy. If it fails, options:
1. Doc: split the deploy — deploy KV first with a separate `az deployment
   group create`, `az keyvault secret set` the pem, then deploy the rest.
2. Code: accept the pem as a `@secure` Bicep param and have `keyVault.bicep`
   create the secret before `containerApp.bicep` references it.

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
