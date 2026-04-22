# Smoke Test — MVP on Azure

End-to-end verification that a fresh CronFoundry deployment on Azure can fire
a scheduled skill, publish to Slack + GitHub, commit a memory update back to
the skill repo, and write audit rows for every mutating action.

Executed once by the author on a fresh subscription. Any step that fails
becomes a fix (code or doc), and the runbook is re-run. The committed version
is the one that worked end to end.

**Depends on:** P6 merged (`docs/superpowers/plans/2026-04-20-p6-mvp-gaps.md`)
for the audit-verification and push-webhook steps.

**Running log of issues found while executing this runbook:**
[`smoke-test-mvp-azure-findings.md`](./smoke-test-mvp-azure-findings.md).

---

## 1. Prerequisites

- An Azure subscription with Contributor rights. `az login` and
  `az account set --subscription <id>` before starting.
- `az` CLI ≥ 2.60. Verify with `az --version`.
- Bicep CLI ≥ 0.26. Install once via `az bicep install` (drops the binary
  under `~/.azure/bin/bicep`). Confirm with `az bicep version`.
- A region where this subscription can actually provision
  **Azure Database for PostgreSQL — Flexible Server**. Microsoft-internal
  subscriptions and some commercial subs restrict this offer per region;
  `az postgres flexible-server list-skus --location <region>` is **not**
  a reliable test (it surfaces the SKU catalog, not your
  provisioning rights). The only reliable probe is a *synchronous*
  `az postgres flexible-server create` — `--no-wait` returns exit 0 even
  when provisioning later fails with `LocationIsOfferRestricted`. If
  unsure, start with a known-good region for your sub
  (e.g., `swedencentral` was the working region for the Microsoft-
  internal sub used on the initial smoke).
- A GitHub account that can register a new GitHub App.
- **One** LLM key from OpenAI, Anthropic, or Azure AI Foundry.
- A Slack **Incoming Webhook URL** for any channel. Create one at
  https://api.slack.com/apps.
- Two GitHub repos under the same owner:
  1. A **skill repo** containing `cronfoundry.yaml` + one `SKILL.md`. You can
     copy `testdata/` from this repo into a new private repo to get going.
  2. A **reports repo** where the `github-issue` destination will file.
- Local clone of this repo (needed for the Bicep deploy in §3).

## 2. Register the GitHub App

> Register a **GitHub App**, not an **OAuth App**. Both live under
> *Settings → Developer settings*, but only GitHub Apps have an App ID,
> installations, and the server-to-server private key (the `.pem`) that
> CronFoundry uses to sign JWTs for GitHub API calls. OAuth Apps expose
> Client ID + Client Secret only and will not satisfy the Bicep's
> `githubAppId` param.

1. Open the "New GitHub App" form for your account:
   - Personal: https://github.com/settings/apps/new
   - Organization: https://github.com/organizations/`<org>`/settings/apps/new

   (Equivalent path through the UI: GitHub → Settings → Developer settings →
   **GitHub Apps** → **New GitHub App**. Check that the URL ends in
   `/settings/apps/new` — if it ends in `/settings/applications/new`
   you're on the OAuth Apps form.)
2. **GitHub App name:** must be globally unique (e.g., `cronfoundry-p7smoke`).
3. **Homepage URL:** anything (e.g., `https://example.com`). You'll fill
   the real hostname after the Bicep deploy in §4.
4. **Callback URL:** `https://example.com/oauth/callback` — placeholder.
5. **Webhook URL:** `https://example.com/webhook/github` — placeholder.
   **Webhook secret:** generate a long random string; save for §5 step 4.
6. **Permissions:**
   - Repository: Contents (R+W), Issues (W), Metadata (R).
   - Account: Email (R).
7. **Subscribe to events:** Push.
8. Save. On the resulting settings page:
   - Note the **App ID** (numeric, shown at the top).
   - Under **Client secrets**, click **Generate a new client secret** — copy
     the value; it's shown once. Note the **Client ID** too (starts with
     `Iv23li…` for GitHub Apps — NOT `Ov23li…` which is OAuth Apps).
   - Under **Private keys**, click **Generate a private key** — your browser
     downloads a `.pem` file. Keep it; you'll upload it to Key Vault in §4d.
9. **Install App** (left sidebar) on your two repos (skill + reports).

You'll come back after §4 to update the three URLs with the real hostname.

## 3. Publish a container image

`deploy/modules/containerApp.bicep` pulls from
`ghcr.io/gambtho/cronfoundry:${imageTag}`. The `release.yml` workflow builds
and pushes multi-arch images, but only on `v*` tags — a fresh checkout with
no release tag has nothing to pull. Container Apps pulls anonymously, so
the GHCR package must also be public.

1. Tag and push:

   ```bash
   git tag v0.7.0
   git push origin v0.7.0
   ```

   The `Release` workflow takes ~8 minutes to build linux/amd64+arm64 and
   push three tags. `docker/metadata-action` uses
   `type=semver,pattern={{version}}`, which **strips the `v` prefix**, so
   the pushed tags are:
   - `ghcr.io/<owner>/cronfoundry:0.7.0`
   - `ghcr.io/<owner>/cronfoundry:0.7`
   - `ghcr.io/<owner>/cronfoundry:latest`

   Watch with `gh run watch`, then verify:

   ```bash
   docker manifest inspect ghcr.io/<owner>/cronfoundry:0.7.0 | head -5
   ```

2. Verify the GHCR package is **Public**. Packages linked to public source
   repositories inherit public visibility by default, so the step above
   should succeed without auth. If it returns `manifest unknown` or
   `denied`, flip the package visibility manually:
   `https://github.com/users/<owner>/packages/container/cronfoundry/settings`
   → Danger Zone → *Change visibility* → Public.

Pick the tag you'll reference from the params file — `latest` works, but
pinning to `0.7.0` makes the deployed version explicit.

## 4. Deploy via Bicep

### 4a. Generate a master key

`masterKey` is used to envelope-encrypt secrets at rest. Any 32 random bytes
encoded as standard base64 work. Fastest:

```bash
openssl rand -base64 32
```

Equivalent, using the binary's built-in generator (requires `make build`):

```bash
./cronfoundry admin init    # prints CRONFOUNDRY_MASTER_KEY=<value> and exits
```

Save the value. If it's ever lost, encrypted secrets in the database are
unrecoverable.

### 4b. Fill the params file

Copy the example and edit it:

```bash
cp deploy/params.example.json deploy/params.p7smoke.json
```

`deploy/params.p7smoke.json`:

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env":                         { "value": "p7smoke" },
    "location":                    { "value": "eastus" },
    "imageTag":                    { "value": "0.7.0" },
    "githubAppId":                 { "value": "<APP_ID from §2>" },
    "githubAppOAuthClientId":      { "value": "<CLIENT_ID from §2>" },
    "githubAppOAuthClientSecret":  { "value": "<CLIENT_SECRET from §2>" },
    "postgresAdminPassword":       { "value": "<20+ char alphanumeric — no @ : / % # ? & =>" },
    "masterKey":                   { "value": "<output of §4a>" },
    "adminLogins":                 { "value": "<your-github-login>" },
    "viewerLogins":                { "value": "" },
    "ingressExternal":             { "value": true }
  }
}
```

Param-by-param reality check:
- `env` — suffix for every resource name; keep it short (≤ 10 chars).
- `imageTag` — whatever you pushed in §3. `latest` works; pinning is better.
- `githubAppId` / `githubAppOAuthClientId` / `githubAppOAuthClientSecret` —
  from the GitHub App settings page in §2. The client secret is shown once
  at creation.
- `postgresAdminPassword` — ends up in a connection string; avoid any
  character `urlencode` would touch. Alphanumerics are safest.
- `masterKey` — the base64 string from §4a. Paste verbatim (trailing `=`
  included).
- `adminLogins` — a **comma-separated string** (not a JSON array). Users in
  this list can mutate everything.
- `ingressExternal` — **must** be `true` for GitHub's webhook to reach the
  API. The default is `false` and silently breaks the smoke.

### 4c. Run the deploy

```bash
az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters @deploy/params.p7smoke.json
```

Takes ~10 minutes. The Key Vault, Postgres, Container Apps Environment,
identities, Container App, and the runner Job are all created.

### 4d. Upload the GitHub App private key to Key Vault

`containerApp.bicep` references a Key Vault secret named `github-app-pem`.
Key Vault itself is created by the deploy, but the secret is not — the
Container App reads the pem at process start, so upload it now:

```bash
KV_NAME=$(az keyvault list \
  --resource-group rg-cronfoundry-p7smoke \
  --query "[0].name" -o tsv)

az keyvault secret set \
  --vault-name "$KV_NAME" \
  --name github-app-pem \
  --file /path/to/your-app.private-key.pem

az containerapp revision restart \
  --resource-group rg-cronfoundry-p7smoke \
  --name api \
  --revision "$(az containerapp revision list \
    --resource-group rg-cronfoundry-p7smoke \
    --name api --query '[0].name' -o tsv)"
```

### 4e. Record the API FQDN and update the GitHub App

```bash
az containerapp show \
  --resource-group rg-cronfoundry-p7smoke \
  --name api \
  --query properties.configuration.ingress.fqdn -o tsv
```

Go back to the GitHub App settings and replace `<your-api-hostname>` in
Homepage URL, Callback URL, and Webhook URL with this FQDN. Save.

## 5. First-boot config (web UI)

Do the repo + secrets setup through the web UI so each action emits the
`repo.connect` and `secret.create` audit events the verification step in §9
checks for. CLI-based setup (`./cronfoundry admin connect-repo` etc.) bypasses
those handlers and would yield no audit rows.

1. Open `https://<fqdn>/`. Log in via GitHub — the `auth.login` audit row lands.
2. **Repos → Connect repo.** Paste `<owner>/<skill-repo>` and the installation
   ID from the GitHub App's installation page. Emits `repo.connect`.
3. **Secrets → Add.** Create three:
   - `llm_key` → your OpenAI / Anthropic / Azure AI Foundry key
   - `slack_webhook` → the Slack Incoming Webhook URL
   - `github_webhook_secret` → the long random string from §2 step 4

   Each creation emits a `secret.create` audit row.
4. Set the same webhook secret as an env var on the API Container App (the
   webhook endpoint reads it from `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`):

   ```bash
   az containerapp update \
     --resource-group rg-cronfoundry-p7smoke \
     --name api \
     --set-env-vars CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=<same value>
   ```

## 6. Land a skill

In your skill repo's `cronfoundry.yaml`, define one schedule firing every 5
minutes:

```yaml
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: every-5
        cron: "*/5 * * * *"
        timezone: UTC
        overlap_policy: skip
        timeout_sec: 300
        provider: openai        # or anthropic / azure-foundry
        model: gpt-4o-mini
        destinations:
          - slack:
              secret: slack_webhook
              text: "{{ output.truncated 35000 }}"
          - github-issue:
              repo: <owner>/<reports-repo>
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

And `skills/smoke/SKILL.md`:

```markdown
---
name: smoke
description: Proves a run end-to-end
max_tokens: 500
---
Write one short paragraph confirming this pipeline works.
End with:

<memory>
run at {{ run.started_at }}
</memory>
```

Commit and push. The push webhook re-syncs the schedule within seconds.

## 7. Observe the first fire

1. You're already logged in from §5. Dashboard shows the new `every-5` schedule.
2. Wait up to 5 minutes for the first natural fire — or click **Run now**.
3. Go to **Runs**, click the newest row.
4. Confirm the **log panel streams** with row levels `info/warn/error` and
   event types (`llm.start`, `llm.chunk.batched`, `publish.slack.ok`,
   `publish.github-issue.ok`, `writeback.commit.ok`). Status transitions to
   `succeeded`.

## 8. Verify the three side effects

- **Slack:** message lands in the configured channel with the skill output.
- **GitHub issue:** a new issue exists in the reports repo, titled
  `smoke — YYYY-MM-DD`, labeled `smoke`.
- **Writeback commit:** the skill repo's `memory.md` has a new line; commit
  author is `cronfoundry[bot]` with message
  `chore(cronfoundry): update memory.md from run <uuid>`.

## 9. Verify the audit log

Navigate to **Audit** in the sidebar (shipped in P6c). Confirm rows are
present for the session you just walked through:

| Action | Target |
|---|---|
| `auth.login` | your github login |
| `repo.connect` | `<owner>/<skill-repo>` |
| `secret.create` | `llm_key` |
| `secret.create` | `slack_webhook` |
| `secret.create` | `github_webhook_secret` |
| `schedule.run_now` | `every-5` (only if you clicked **Run now**) |

If any row is missing, file it as a P6c fix and do not mark the smoke passed.

## 10. Teardown

```bash
az group delete --name rg-cronfoundry-p7smoke --yes --no-wait
```

Revoke the GitHub App installation and delete the App registration once the
resource group is gone.

---

## Pass/fail checklist

- [ ] One `succeeded` run visible in the dashboard.
- [ ] Slack message present.
- [ ] GitHub issue filed in the reports repo.
- [ ] `memory.md` commit authored by `cronfoundry[bot]`.
- [ ] Audit log contains login + repo-connect + secret-create rows.
- [ ] Run detail shows non-zero `tokens_in`, `tokens_out`, and `cost_cents`
      (cost accounting from the P7 follow-up).

If every box is checked, MVP is shipped. Otherwise, every unchecked box
becomes a fix in code or docs and the runbook re-runs.
