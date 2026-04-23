# CronFoundry — Multi-Platform Deploy Design

**Status:** Proposed
**Date:** 2026-04-22
**Author:** gambtho (brainstormed with Claude)

## Overview

This spec covers deferred items #10 (Helm / AKS deploy path) and #11 (multi-cloud adapters) from the MVP design. The goal is to give users who don't want or have Azure Container Apps an alternative runtime, with first-class support for:

1. **AKS** — Kubernetes Jobs + Azure Key Vault + workload identity
2. **Fly.io** — Fly Machines + Postgres-backed encrypted secret store + API key identity

AWS and GCP adapters are explicitly deferred as fast-follow work; the interface design accommodates them without further structural changes.

## Goals

- Users who prefer AKS or Fly.io can self-host CronFoundry without any Azure Container Apps dependency.
- The same runner image works across all platforms — no platform-specific runner builds.
- Platform selection is explicit config, not magic detection.
- AKS deployment can reuse Azure Key Vault (no new secrets infrastructure required).
- Fly.io deployment requires no external secrets service — secrets stored encrypted in the existing Postgres DB.
- The existing Azure Container Apps path is unchanged.

## Non-Goals

- AWS (ECS/Fargate) and GCP (Cloud Run Jobs) adapters — deferred fast-follow.
- Render — no on-demand job primitive; not a fit.
- Cross-platform secret migration tooling.
- Multi-platform active-active or failover.

## Architecture

### Composable Adapter Interfaces

The existing `internal/cloud/` stub is replaced with three independent interfaces. Each concern is selected and wired independently at startup.

```go
// JobDispatcher launches an ephemeral runner for a given run ID.
type JobDispatcher interface {
    DispatchJob(ctx context.Context, req DispatchRequest) error
}

// SecretStore reads and writes named secrets.
type SecretStore interface {
    GetSecret(ctx context.Context, ref string) (string, error)
    SetSecret(ctx context.Context, name, value string) (ref string, error)
    DeleteSecret(ctx context.Context, ref string) error
}

// IdentityProvider mints short-lived tokens for outbound calls.
type IdentityProvider interface {
    GetToken(ctx context.Context, resource string) (string, error)
}
```

### Package Structure

```
internal/
  cloud/
    interfaces.go          # the three interfaces + DispatchRequest type
    config.go              # wires adapters from config at startup
  secretstore/
    azure/                 # existing Key Vault adapter
    postgres/              # new: AES-256-GCM encrypted secrets in DB
  jobdispatch/
    containerappsjobs/     # existing Azure adapter (moved/renamed)
    k8sjobs/               # new: Kubernetes Jobs
    flymachines/           # new: Fly Machines API
  identity/
    managedidentity/       # existing Azure managed identity
    workloadidentity/      # new: K8s workload identity (federated OIDC)
    apikey/                # new: static API key (Fly.io)
```

### Config

Platform is selected by combining one adapter per concern:

```yaml
dispatch:  k8sjobs          # containerappsjobs | k8sjobs | flymachines
secrets:   azure            # azure | postgres
identity:  workloadidentity # managedidentity | workloadidentity | apikey
```

No named platform bundles — each adapter is chosen explicitly. This is slightly more verbose but unambiguous and independently testable.

## Per-Platform Adapter Details

### AKS

**Dispatch — `k8sjobs`**

Creates a `batch/v1 Job` via the in-cluster Kubernetes API. One container, env vars `RUN_ID` + `SCHEDULE_ID`, resource limits from the schedule config, `ttlSecondsAfterFinished` for automatic cleanup. Runner image from config. No static Job manifests — Jobs are created dynamically by the scheduler.

**Secrets — `azure` (reused unchanged)**

AKS pods with workload identity can already reach Azure Key Vault. No new secret infrastructure required.

**Identity — `workloadidentity`**

Exchanges a K8s projected service account token for an Azure AD token via federated credential (standard AKS + Azure OIDC). No code change to how secrets are fetched downstream.

**Config block:**

```yaml
dispatch:  k8sjobs
secrets:   azure
identity:  workloadidentity

k8sjobs:
  namespace:       cronfoundry
  runner_image:    ghcr.io/cronfoundry/runner:v1.2.0
  service_account: cf-runner   # has federated credential to cf-runner managed identity
```

### Fly.io

**Dispatch — `flymachines`**

Calls the Fly Machines REST API (`POST /v1/apps/{app}/machines`) with `auto_destroy: true`. The runner Fly app must be pre-created (one-time setup, documented). Fly API token provided via `FLY_API_TOKEN` env var at deploy time.

**Secrets — `postgres`**

Stores encrypted blobs in a new `encrypted_secret` table in the existing Postgres DB. Encryption: AES-256-GCM, key from `SECRET_STORE_KEY` env var (user-provided at deploy, never stored in DB). The `ref` returned by `SetSecret` is the row UUID — same opaque ref shape the rest of the system already handles. The UI secret management experience is unchanged.

**Identity — `apikey`**

Fly has no identity service. The runner authenticates to the API's `/internal` endpoints using a pre-shared `RUNNER_API_KEY` env var instead of a managed identity JWT. When `identity: apikey`, the API key auth path is enabled; it is disabled entirely on other identity configs to prevent downgrade.

**Config block:**

```yaml
dispatch:  flymachines
secrets:   postgres
identity:  apikey

flymachines:
  app:            cronfoundry-runner
  runner_image:   registry.fly.io/cronfoundry-runner:v1.2.0
  api_token_env:  FLY_API_TOKEN
```

## Data Model Changes

One new migration — `encrypted_secret` table (Fly.io secret store only; ignored when `secrets: azure`):

```sql
CREATE TABLE encrypted_secret (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES organization(id),
    name         TEXT NOT NULL,
    ciphertext   BYTEA NOT NULL,
    nonce        BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    UNIQUE (org_id, name)
);
```

`ref` stored in `schedule.keyvault_ref_*` and `schedule.env_secret_refs_json` is the row UUID as a string — same opaque shape as Azure Key Vault refs. No schema changes elsewhere.

## Internal API Changes

**Dual authentication on `/internal` endpoints**

Today the runner authenticates via managed identity JWT. With `identity: apikey`, a pre-shared key is used instead. The change is a middleware addition:

```
Auth middleware: check Authorization header
  → Bearer token:    validate as managed identity JWT (existing path)
  → X-Runner-Key:    validate against RUNNER_API_KEY env var (new path, only when identity: apikey)
  → else:            401
```

When `identity` is `managedidentity` or `workloadidentity`, the `X-Runner-Key` path is disabled — no downgrade attack surface.

**No changes to runner, scheduler, or publish packages.** They consume the three interfaces, not concrete adapters.

## Deployment Story

### AKS — `deploy/aks/`

```
deploy/
  aks/
    chart/
      Chart.yaml
      values.yaml
      templates/
        deployment-api.yaml
        deployment-scheduler.yaml
        serviceaccount.yaml    # workload identity annotation
        configmap.yaml         # cloud config block
        secret.yaml            # RUNNER_API_KEY if needed (not for AKS)
    README.md
```

Prerequisites documented in `README.md`: OIDC issuer enabled on AKS cluster, workload identity addon installed, Azure Key Vault + federated credential configured, GHCR image pull secret. Deploy: `helm install cronfoundry deploy/aks/chart -f my-values.yaml`.

Postgres is user-provided — Azure Database for PostgreSQL, Amazon RDS, or in-cluster Postgres. CronFoundry has no opinion.

### Fly.io — `deploy/fly/`

```
deploy/
  fly/
    fly.api.toml        # API + UI app
    fly.runner.toml     # runner app (pre-created, no persistent processes)
    README.md
```

`README.md` covers: `flyctl` prerequisites, `fly postgres create` or external Postgres setup, required env vars (`SECRET_STORE_KEY`, `RUNNER_API_KEY`, `FLY_API_TOKEN`, `DATABASE_URL`, etc.), and `flyctl deploy` steps for both apps.

### CI/CD

No changes to the existing GitHub Actions workflow. AKS pulls from GHCR directly. Fly.io users push to `registry.fly.io` via `flyctl deploy --image ghcr.io/cronfoundry/...` or add a Fly API token secret to their repo and let the workflow push.

## Security Notes

- `SECRET_STORE_KEY` for Postgres encryption is never stored in the DB. Loss of this key means loss of all secrets in the Postgres store — operators must back it up independently. Rotation requires re-encrypting all secrets (documented procedure, not automated at MVP).
- `RUNNER_API_KEY` should be a high-entropy random string (32+ bytes). Documented in setup guide.
- The `apikey` identity path is disabled by default and only enabled when `identity: apikey` is explicitly set.
- AKS workload identity follows the standard Azure + OIDC federation pattern — no long-lived credentials in the cluster.

## Deferred Fast-Follow

- **AWS**: ECS/Fargate dispatch + AWS Secrets Manager + IAM task role. Adapters slot into the same three interfaces with no structural change.
- **GCP**: Cloud Run Jobs dispatch + GCP Secret Manager + workload identity federation. Same.
- **Fly.io secret key rotation**: automated re-encryption of all secrets on key change.
- **KV-proxy sidecar**: cryptographic per-run secret scoping (already deferred in MVP design, still deferred here).

## Success Criteria

- A user on AKS can deploy CronFoundry with `helm install` and have a schedule fire end-to-end, with secrets in Azure Key Vault, without touching Container Apps.
- A user on Fly.io can deploy CronFoundry with `flyctl deploy`, store secrets via the UI, and have a schedule fire end-to-end with no external secrets service.
- The existing Azure Container Apps deployment is unchanged and all existing tests pass.
- Each adapter (k8sjobs, flymachines, postgres secretstore, workloadidentity, apikey) has its own unit tests with mocked dependencies.
