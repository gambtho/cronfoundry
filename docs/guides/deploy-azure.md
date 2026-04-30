# Deploying CronFoundry to Azure

## Prerequisites

- Azure CLI (`az`) logged in: `az login`
- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Docker (for local testing only)
- A GitHub App registered (see below)
- A GitHub OAuth App or the same GitHub App's OAuth credentials

## 1. Register a GitHub App

1. Go to https://github.com/settings/apps/new
2. Set name: `cronfoundry-<yourname>`
3. Homepage URL: your fork or self-hosted URL (e.g. `https://github.com/yourname/cronfoundry`)
4. Callback URL: `https://<your-serve-fqdn>/oauth/callback` (update after deploy)
5. Webhook URL: `https://<your-serve-fqdn>/webhook/github` (update after deploy)
6. Permissions:
   - Repository contents: Read & write
   - Issues: Write
7. Generate and download the private key PEM file
8. Note the App ID and OAuth client ID/secret

## 2. Prepare parameters

```bash
cp deploy/params.example.json deploy/params.json
# Edit deploy/params.json with your values
# Never commit deploy/params.json (it contains secrets)
```

Required-in-production parameters worth calling out:

| Param | Notes |
|---|---|
| `ingressExternal` | Set `true` for any deploy that needs the GitHub push webhook to reach it. The default `false` produces an internal-only FQDN. |
| `trustProxy` | Set `true` for any deploy behind a reverse proxy or Container Apps ingress so the leftmost `X-Forwarded-For` is used for rate limiting. The default `false` makes the limiter see the proxy IP and uselessly limit one shared bucket. |

### Rate-limit tuning (rarely needed)

The serve container reads these env vars at startup. Defaults match the
release-readiness sizing for a single-operator deploy:

| Env var | Default | What it controls |
|---|---|---|
| `CRONFOUNDRY_RATE_API_RPM` | 60 | Per-IP `/api/*` requests per minute |
| `CRONFOUNDRY_RATE_OAUTH_RPM` | 10 | Per-IP `/oauth/login` + `/oauth/callback` per minute |
| `CRONFOUNDRY_RATE_WEBHOOK_RPM` | 300 | Per-IP `/webhook/github` per minute (sized for GitHub fan-out) |
| `CRONFOUNDRY_RATE_SSE_MAX_CONCURRENT` | 5 | Concurrent live-tail streams per IP |
| `CRONFOUNDRY_RATE_LRU_SIZE` | 4096 | Per-group LRU map size (memory bound) |
| `CRONFOUNDRY_RATE_DISABLED` | false | Kill switch — middleware passes through entirely |

Set any RPM to `0` to disable rate limiting on that group only. These are
operator overrides not exposed as Bicep params; set them via
`containerApp.bicep`'s env block if you need persistent values.

## 3. Deploy

```bash
# Using azd (recommended):
azd up

# Or directly with Azure CLI (--name main ensures you can reference outputs later):
az deployment sub create \
  --name main \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters deploy/params.json
```

## 4. Post-deploy: store the GitHub App PEM

```bash
KV_NAME=$(az deployment sub show -n main \
  --query "properties.outputs.kvUrl.value" -o tsv \
  | sed 's|https://||;s|.vault.azure.net/||')

az keyvault secret set \
  --vault-name "$KV_NAME" \
  --name github-app-pem \
  --file /path/to/github-app.pem
```

## 5. Initialize the database

CronFoundry auto-runs migrations on startup. Seed the first organization.

> **Note:** Replace `cf-serve-prod` and `rg-cronfoundry-prod` with the actual names derived from
> your `prefix` and `env` params (defaults: prefix=`cf`, env=`prod`).

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin init --org-name myorg"
```

## 6. Connect a repo

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin connect-repo --install-id <github_install_id> --owner myorg --repo myrepo"
```

## Upgrading

```bash
az containerapp update \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --image ghcr.io/gambtho/cronfoundry:v0.X.Y

az containerapp job update \
  --name cf-runner-prod \
  --resource-group rg-cronfoundry-prod \
  --image ghcr.io/gambtho/cronfoundry:v0.X.Y
```

## Teardown

> **WARNING:** `azd down` deletes the entire resource group including the Postgres database. Back up first.
>
> **Key Vault note:** If `enablePurgeProtection` was set to `true` in params, the soft-deleted vault
> cannot be purged for `softDeleteRetentionDays` (default 7). Re-deploying with the same `prefix`/`env`
> will fail with a vault name conflict until the retention window expires.

```bash
azd down
```

## Enabling the Web UI

Once the React UI is deployed, open ingress to the public:

```bash
az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters deploy/params.json \
  --parameters ingressExternal=true
```
