# Deploy CronFoundry to Azure (Reference)

The happy path is in [`quickstart-azure.md`](./quickstart-azure.md). This
doc covers non-default deployments, operational tuning, and troubleshooting.

## Manual deploy (without `cronfoundry bootstrap azure`)

If you can't or don't want to use the bootstrap command:

1. **Generate a master key:** `openssl rand -base64 32`. Save it; encrypted
   secrets in the database become unrecoverable if it's lost.
2. **Copy and edit the params file:**

   ```bash
   cp deploy/params.example.json deploy/params.prod.json
   ```

   Fill in:
   - `githubAppId`, `githubAppOAuthClientId`, `githubAppOAuthClientSecret`
   - `postgresAdminPassword` — 24+ alphanumerics, no `@ : / % # ? & =`
   - `masterKey` — your generated key
   - `githubAppPem` — full contents of the .pem file (use a small Python
     helper to embed newlines as `\n`)
   - `adminLogins` — comma-separated GitHub logins (not a JSON array)
   - `ingressExternal` — leave at `true` unless fronting with a private
     gateway

3. **Deploy:**

   ```bash
   az deployment sub create \
     --location swedencentral \
     --template-file deploy/main.bicep \
     --parameters @deploy/params.prod.json
   ```

   Takes ~10 minutes.

4. **Open Postgres to your IP:**

   ```bash
   MY_IP=$(curl -s https://ifconfig.me)
   az postgres flexible-server firewall-rule create \
     --resource-group rg-cronfoundry-<env> --name cf-pg-<env> \
     --rule-name op --start-ip-address "$MY_IP" --end-ip-address "$MY_IP"
   ```

5. **Run admin init locally:**

   ```bash
   CRONFOUNDRY_DATABASE_URL="postgres://cfadmin:<pw>@cf-pg-<env>.postgres.database.azure.com:5432/cronfoundry?sslmode=require" \
   CRONFOUNDRY_MASTER_KEY="<your master key>" \
   ./cronfoundry admin init
   ```

6. **Force a serve revision restart so the migrated schema is picked up:**

   ```bash
   az containerapp update \
     --resource-group rg-cronfoundry-<env> --name cf-serve-<env> \
     --set-env-vars RESTART_TRIGGER=$(date +%s)
   ```

7. **Discover the FQDN:**

   ```bash
   az containerapp show \
     --resource-group rg-cronfoundry-<env> --name cf-serve-<env> \
     --query properties.configuration.ingress.fqdn -o tsv
   ```

## Required Bicep parameters worth calling out

| Param | Notes |
|---|---|
| `ingressExternal` | Set `true` for any deploy that needs the GitHub push webhook to reach it. The default `false` produces an internal-only FQDN. The bootstrap command and `params.example.json` set this to `true`. |
| `trustProxy` | Set `true` for any deploy behind a reverse proxy or Container Apps ingress so the leftmost `X-Forwarded-For` is used for rate limiting. The default `false` makes the limiter see the proxy IP and uselessly limit one shared bucket. |
| `publicBaseUrl` | Externally-reachable URL of the service (scheme+host, e.g. `https://cronfoundry.example.com`). Used as the CSRF middleware `Origin`/`Referer` allowlist. Empty disables the Origin check (local dev only); the cookie+header double-submit check still runs. After the first deploy, find the FQDN with `az containerapp show -n <name> -g <rg> --query properties.configuration.ingress.fqdn -o tsv` and re-deploy with that value set. |

## Region selection

`Microsoft.DBforPostgreSQL/flexibleServers` is offer-restricted in some
subscriptions. The reliable probe is a synchronous create; the listing
APIs return SKUs your subscription cannot actually provision.
`swedencentral` is known-good for Microsoft-internal subscriptions;
`eastus`/`eastus2` were observed restricted.

## Rate-limit tuning (rarely needed)

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

## Custom domain / VNet

Out of the box the Container App uses the
`<env>.<random>.azurecontainerapps.io` FQDN. To put it behind a custom
domain, follow the
[Container Apps custom domain docs](https://learn.microsoft.com/azure/container-apps/custom-domains-certificates)
after the bootstrap completes. The Bicep does not yet wire VNet
integration; pass real `subnetId`/`privateDnsZoneId` to
`modules/postgres.bicep` if you need it.

## Upgrading the image

The serve container, the runner Container App Job, **and** the
`AZURE_CAE_JOB_IMAGE` env var on the serve container all need to advance
together — `AZURE_CAE_JOB_IMAGE` is what the dispatcher uses when it
spawns runner executions, and a stale value silently runs old code on
new dispatches.

```bash
TAG=0.X.Y
RG=rg-cronfoundry-<env>
ENV=<env>

az containerapp update --resource-group "$RG" --name "cf-serve-$ENV" \
  --image "ghcr.io/gambtho/cronfoundry:$TAG" \
  --set-env-vars "AZURE_CAE_JOB_IMAGE=ghcr.io/gambtho/cronfoundry:$TAG"
az containerapp job update --resource-group "$RG" --name "cf-runner-$ENV" \
  --image "ghcr.io/gambtho/cronfoundry:$TAG"
```

## Teardown

```bash
az group delete --name rg-cronfoundry-<env> --yes --no-wait
```

Note: Key Vault and Postgres soft-delete pin the resource names for 7 days.
Re-deploys with the same `env` need to wait the retention window or use a
different suffix.

## Troubleshooting

- **`az deployment sub create` complains about `ingressExternal`** — the
  default `params.example.json` ships `true`; if you flipped it, the GitHub
  webhook can't reach your API. Set `true` and redeploy.
- **`docker manifest inspect ghcr.io/gambtho/cronfoundry:v0.7.0` returns
  `manifest unknown`** — `docker/metadata-action` strips the `v` prefix.
  Use `0.7.0`, `0.7`, or `latest`.
- **`LocationIsOfferRestricted`** — your subscription can't provision
  Postgres Flexible Server in this region. Try `swedencentral`.
- **GHCR pull fails with `denied`** — the package may be private. Visit
  `https://github.com/users/<owner>/packages/container/cronfoundry/settings`
  → Danger Zone → *Change visibility* → Public.
- **OAuth Client ID starts with `Ov23li`, not `Iv23li`** — you registered
  an OAuth App, not a GitHub App. Start over at
  `https://github.com/settings/apps/new`.
- **Container App stuck on the previous revision after `admin init`** —
  trigger a restart with `--set-env-vars RESTART_TRIGGER=$(date +%s)`.
- **Rate limiter treats every request as the same IP** — set
  `trustProxy: true` so the limiter reads the leftmost `X-Forwarded-For`.
- **CSRF requests rejected with `bad origin`** — set `publicBaseUrl` to
  the externally-reachable URL (scheme+host) and redeploy.

## Maintainers: end-to-end smoke test

Periodic full-deploy testing is documented in `internal/bootstrap/azure/`
(unit-tested under `go test ./...`; an integration test gated on
`CRONFOUNDRY_E2E=1` exercises a real subscription). Historical
play-by-plays are in `.smoke-history/`.
