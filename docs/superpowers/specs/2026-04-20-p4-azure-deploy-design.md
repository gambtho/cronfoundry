# P4 — Azure Deployment — Design

**Status:** Proposed
**Date:** 2026-04-20
**Author:** gambtho (brainstormed with Claude)

## Overview

P4 moves CronFoundry from "runs on my laptop with docker-compose" to "runs on Azure with a one-command deploy." It ships in three sequential tracks:

- **P4a** — Azure Go implementations: `ContainerAppsJobDispatcher` and `KeyVaultStore` behind the existing interfaces
- **P4b** — Bicep IaC + GitHub Actions CI/CD
- **P4c** — Operator docs + smoke test

The `internal/cloud/` and `internal/secretstore/` abstraction layers are already in place from P2, so P4 adds concrete Azure implementations without touching application logic.

## Foundation (already built — P2)

- `internal/cloud/dispatcher.go` — `JobDispatcher` interface
- `internal/cloud/subprocess.go` — `SubprocessDispatcher` (local dev / docker-compose)
- `internal/cloud/azure/dispatcher.go` — stub `ContainerAppsJobDispatcher` (P4a fills this in)
- `internal/secretstore/secretstore.go` — `SecretStore` interface
- `internal/secretstore/postgres.go` — `EnvelopePostgresStore` (local dev)
- `internal/secretstore/azure/keyvault.go` — stub `KeyVaultStore` (P4a fills this in)

At startup, `cronfoundry serve` selects implementations via environment variables:

```
DISPATCHER=azure|subprocess       (default: subprocess)
SECRET_STORE=keyvault|postgres    (default: postgres)
```

`docker-compose` dev environment keeps both defaults so local dev requires no Azure subscription.

## P4a — Azure Go Implementations

### `ContainerAppsJobDispatcher`

**Location:** `internal/cloud/azure/dispatcher.go`

Uses `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers` to call `POST /jobs/{runner}/start` with env vars `RUN_ID` and `SCHEDULE_ID`. Authenticates via `azidentity.DefaultAzureCredential` (picks up managed identity in Azure; falls back to CLI credentials locally with a real subscription).

Required env vars:
```
AZURE_SUBSCRIPTION_ID
AZURE_RESOURCE_GROUP
AZURE_CONTAINER_APPS_JOB_NAME   # the runner job resource name
```

**Testing:** unit-tested against the `ARMClient` interface already stubbed in `internal/cloud/azure/armclient.go`.

### `KeyVaultStore`

**Location:** `internal/secretstore/azure/keyvault.go`

Uses `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets`. Implements `Get`, `Set`, `Delete`, `List` on the `SecretStore` interface. Authenticates via `azidentity.DefaultAzureCredential`.

Required env var:
```
AZURE_KEYVAULT_URL   # e.g. https://cf-kv-prod.vault.azure.net/
```

Secret names use the KV reference format from `internal/secretstore/envelope.go`: `vault_name/secret_name/version` (version optional; empty string = latest).

**Testing:** unit-tested against the `KVClient` interface already stubbed in `internal/secretstore/azure/kvclient.go`.

### Managed Identity Wiring

Two managed identities (scheduler is in-process with API, so three-identity design collapses to two):

**`cf-serve`** (Container App running `cronfoundry serve`):
- `Key Vault Secrets User` on the Key Vault — read secrets at run time
- `Key Vault Certificate User` — read GitHub App PEM stored as a KV certificate
- Postgres RW — either Entra ID auth or connection string from KV
- `Microsoft.App/jobs/executions/write` — dispatch runner jobs

**`cf-runner`** (Container Apps Job running `cronfoundry runner`):
- No direct Azure credentials
- All secrets arrive via `/internal/runs/{id}/context` from the API
- Outbound-only: GitHub, LLM providers, destinations

## P4b — Bicep IaC + CI/CD

### Bicep Template

**Location:** `deploy/main.bicep` (subscription-scoped deployment)

Resources provisioned in `rg-cronfoundry-{env}`:

| Resource | SKU / Config |
|---|---|
| Container Apps Environment | Consumption + Workload Profiles, VNet-integrated |
| Container App `cronfoundry` | 0.5 vCPU, 1 GB, 1–2 replicas, managed identity `cf-serve` |
| Container Apps Job `runner` | 1 vCPU, 2 GB, manual-trigger type, managed identity `cf-runner` |
| Postgres Flexible Server | B1ms burstable, private endpoint, no public access |
| Key Vault | Standard, purge-protect on, soft-delete 30d |
| Log Analytics Workspace | 30d retention; Container Apps logs route here automatically |
| Managed Identities ×2 | `cf-serve`, `cf-runner`; role assignments inline in Bicep |

Parameters file: `deploy/params.json` (template committed; actual values gitignored).

```json
{
  "env": "prod",
  "location": "eastus",
  "githubAppId": "",
  "allowlistLogins": "",
  "imageTag": "latest"
}
```

**`azure.yaml`** wraps the Bicep for `azd up` / `azd down`. `azd down` deletes the resource group — documented with a warning.

### Public Ingress

P4 ships with Container Apps ingress set to **internal** (VNet-only). When P5 (Web UI) is deployed, the operator sets Bicep parameter `ingressExternal: true` and redeploys. This is a one-parameter change, documented in the runbook.

Operators reach the internal-only service via `az containerapp exec` or `az network bastion tunnel`.

### GitHub Actions

**`ci.yml`** (trigger: PR to `main`):
- `go vet ./...`
- `go test ./...` (with Postgres via service container)
- `golangci-lint`
- `make web` + `make build` (build binary, don't push)

**`release.yml`** (trigger: tag `v*`):
- Multi-arch image build (amd64 + arm64) via `docker buildx`
- Push to `ghcr.io/gambtho/cronfoundry:{tag}` and `:latest`
- Attach `deploy/main.bicep` + `deploy/params.json.example` as release artifacts
- Auto-generate release notes from commits since previous tag (`gh release create --generate-notes`)

## P4c — Operator Docs + Smoke Test

**`docs/guides/deploy-azure.md`** — step-by-step first-deploy guide:
1. Prerequisites: Azure CLI, azd, Docker, GitHub App registered
2. `azd up` walkthrough with params
3. Post-deploy: `cronfoundry admin init` equivalent (auto-runs on serve startup)
4. Connect a repo, add a secret, trigger a manual run
5. Upgrade path: bump image tag in Container App + redeploy
6. Teardown: `azd down` warning

**`docs/guides/smoke-test-p4.md`** — checklist parallel to `smoke-test-p2.md`:
- Verify Container App is running and healthy
- Verify scheduler tick is firing (Log Analytics query)
- Trigger a manual run, confirm runner Job starts and completes
- Confirm secret is read from Key Vault (KV access log)
- Confirm run result appears in Postgres

**`docs/guides/observability.md`** — recommended Azure Monitor alert rules:
- Consecutive run failures > 5
- Scheduler tick stalled > 5 min
- Runner OOM rate
- Cost-per-run anomaly

## Implementation Tracks

| Track | Scope | Prerequisite |
|---|---|---|
| P4a | `ContainerAppsJobDispatcher` + `KeyVaultStore` Go implementations | P2 merged |
| P4b | Bicep template + `azure.yaml` + GitHub Actions workflows | P4a (image must exist to reference) |
| P4c | Operator runbook + smoke-test checklist + observability guide | P4b (deploy must work to document) |

## Cost Target

Idle / light-use estimate < $90/month:

| Resource | Est. monthly cost |
|---|---|
| Postgres B1ms | ~$25 |
| Container Apps compute (light scheduled load) | ~$20–30 |
| ACR Basic (optional — GHCR is default) | ~$5 if used |
| Key Vault ops | negligible |
| Log Analytics (< 5 GB/mo) | free tier |
| **Total** | **~$50–60** |

## What Does NOT Change

- `cmd/cronfoundry/serve.go` — reads config from env; P4 sets different env values
- `internal/scheduler/*`, `internal/runner/*`, `internal/api/*` — untouched
- `internal/db/*` — Postgres Flexible Server is wire-compatible
- `internal/token/*` — JWT signing unchanged
- Database schema — no changes

## Success Criteria

An operator with an Azure subscription can:
1. Run `azd up` against a fresh resource group and have all infrastructure provisioned.
2. Set image tags, add GitHub App credentials and an LLM API key as Key Vault secrets.
3. See `cronfoundry serve` start, run migrations, and seed the default org automatically.
4. Trigger a manual run and watch it execute as a Container Apps Job.
5. See the run result and logs in Log Analytics.
6. Upgrade to a new image tag with a single Container App update and redeploy.
