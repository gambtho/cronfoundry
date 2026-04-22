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

## F6 — GHCR image is private by default on first push

**Severity:** blocker
**Type:** doc

First CI push creates a private GHCR package. `containerApp.bicep` uses no
`imagePullSecrets`, so the Container App pulls anonymously and gets a 401
until the package is flipped to public in the GHCR UI.

**Fix:** doc — §3 ends with a step pointing at the GHCR package-settings URL
to flip visibility to Public.

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
