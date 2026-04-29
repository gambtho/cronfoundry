# CronFoundry mvpplus-3: Secure Secret Storage — Design

**Status:** Shipped (3a5fde1)
**Date:** 2026-04-23  
**Author:** gambtho (brainstormed with Claude)

## Overview

mvpplus-3 addresses two secret-handling gaps that limit CronFoundry's suitability
for production use:

1. **LLM exposure** — secret values are currently injected into the LLM prompt
   in plaintext via the `<env>` block, making them visible to the model, present
   in run logs, and potentially captured by LLM provider data pipelines.

2. **Host storage** — secrets are stored as plain environment variables, readable
   by anyone with process access or via `docker inspect`.

The solution is a pluggable secret backend (replacing the env-var-only resolver)
combined with prompt-level redaction (injecting secret names but not values into
LLM context).

---

## Feature Phases Context

| Phase | Features |
|-------|----------|
| mvpplus-1 | UI schedule edits, conditional routing, rich formatting |
| mvpplus-2 | Custom HTTP destinations |
| **mvpplus-3** | **Pluggable secret backends, LLM prompt redaction** |
| Fully deferred | SSO/Entra, Helm/AKS, multi-cloud, hosted SaaS |

---

## Architecture

Two independent concerns compose cleanly:

**1. Pluggable secret backend** — the `secrets` package gains a `Backend`
interface. Multiple implementations cover the full deployment spectrum from
zero-ops (env vars) to cloud-managed (Azure Key Vault, AWS Secrets Manager, GCP
Secret Manager) to self-hosted encrypted storage (encrypted file, Postgres).
The active backend is selected via a `secret_backend` stanza in app config.
The existing `Resolver` wrapper is retained but delegates to the backend.

**2. LLM prompt redaction** — `buildEnvBanner` is modified to emit
`KEY=[secret]` for any env entry backed by a secret ref, rather than resolving
and injecting the raw value. Secret values are resolved lazily: only at MCP tool
env injection (`resolveServerEnv`) and destination dispatch — the two points
where the value is actually needed. The existing redactor pipeline (`AllValues()`
+ output scrubbing) is unchanged in structure; cloud backends that cannot
enumerate extend it by caching every value fetched during a run.

These two changes are independent and can be implemented in either order.

---

## Secret Backend Interface

```go
// Backend retrieves secret values by name.
type Backend interface {
    Get(ctx context.Context, name string) (string, error)
}
```

`Resolver` retains its public API (`Get`, `AllValues`) but holds a `Backend`
instead of a raw env map. `AllValues()` is best-effort: backends that can
enumerate (env, encrypted file, Postgres) return all values; backends that
cannot (cloud managers, Vault) return only values fetched during the current
run, held in an in-memory cache on the resolver.

### Implementations

| Backend | Type key | Notes |
|---------|----------|-------|
| `EnvBackend` | `env` | Default; current behavior unchanged |
| `EncryptedFileBackend` | `encrypted_file` | JSON file, AES-256-GCM, key from config or env |
| `PostgresBackend` | `postgres` | `cf_secrets` table, AES-256-GCM, uses existing DB connection |
| `AzureKeyVaultBackend` | `azure_keyvault` | Azure SDK, workload identity or client secret |
| `AWSSecretsManagerBackend` | `aws_secrets_manager` | AWS SDK |
| `GCPSecretManagerBackend` | `gcp_secret_manager` | GCP SDK |
| `VaultBackend` | `vault` | HashiCorp Vault KV v2 via HTTP API |

### Config Shape

```yaml
secret_backend:
  type: azure_keyvault                              # required; defaults to "env" if omitted
  vault_url: https://myvault.vault.azure.net        # azure_keyvault, vault
  file_path: /etc/cronfoundry/secrets.enc           # encrypted_file
  encryption_key: "<hex-encoded 32-byte key>"       # encrypted_file, postgres
  # encryption_key may also be set via CRONFOUNDRY_SECRET_BACKEND_KEY env var
```

---

## LLM Prompt Redaction

**Change to `buildEnvBanner`** (`internal/runner/runner.go:412`):

Secret-backed env entries emit a placeholder instead of the resolved value:

```
<env>
GITHUB_TOKEN=[secret]
API_BASE_URL=https://api.example.com
</env>
```

The LLM sees the key name (so it can reason about available credentials) but
never the value.

**Late resolution points** — secret values are resolved only at:

1. `resolveServerEnv` (MCP tool server env injection) — already calls `s.Get()`;
   no structural change, now goes through the backend
2. `publish/dispatcher.go` (destination credentials) — already calls `s.Get()`
   at dispatch time; no structural change

**Redactor cache** — the `Resolver` accumulates every value returned by
`Backend.Get()` during a run. `AllValues()` returns this cache union any
enumerable values from the backend. This ensures values that reached the LLM
via a tool response are scrubbed from run output.

---

## Data Model (PostgresBackend)

```sql
CREATE TABLE cf_secrets (
    name        TEXT PRIMARY KEY,
    ciphertext  BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

The encryption key never enters the database — only ciphertext is stored.
AES-256-GCM provides authenticated encryption; decryption failure (wrong key,
tampered ciphertext) returns an error that fails the run.

### Management API

Available when `secret_backend.type` is `postgres` or `encrypted_file` (secrets
are managed externally for cloud/Vault backends):

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/secrets` | Create or update a secret (name + plaintext; encrypted before write) |
| `DELETE` | `/api/secrets/{name}` | Remove a secret |
| `GET` | `/api/secrets` | List secret names only — values never returned |

All endpoints require admin auth (existing middleware).

A new numbered migration creates `cf_secrets`. No changes to existing tables.

---

## Error Handling

- **Backend initialization failures** (missing config, unreachable service, invalid
  key) are fatal at startup — CronFoundry logs and exits rather than running with
  broken secret resolution.
- **Per-secret resolution failures** (not found, permission denied) fail the run
  with error kind `secret_resolve`, consistent with the existing `mcp_env_resolve`
  pattern.
- **Transient errors** on cloud/Vault backends: 3 retries with exponential backoff.
  Permanent errors (not found, auth failure) fail immediately without retry.

---

## Testing

| Area | Approach |
|------|----------|
| `EnvBackend` | Existing resolver tests migrate to new interface |
| `PostgresBackend` | Integration tests using existing test DB; encrypt/decrypt roundtrip + management API endpoints |
| Cloud backends | Unit tested with interface mocks; no live cloud calls in CI |
| `EncryptedFileBackend` | Unit tested with a temp file |
| LLM prompt redaction | `buildEnvBanner` tests assert `[secret]` placeholder present, raw values absent |
| Redactor cache | Test that values fetched via non-enumerable backends appear in `AllValues()` |

---

## Success Criteria

- A skill with `env: { GITHUB_TOKEN: { secret: github_pat } }` configured shows
  `GITHUB_TOKEN=[secret]` in the LLM prompt and the raw value does not appear in
  run logs.
- Switching `secret_backend.type` from `env` to `postgres` requires no changes to
  `cronfoundry.yaml` skill configs — secret names are unchanged.
- A misconfigured backend (wrong vault URL, missing key) causes CronFoundry to
  refuse to start with a clear error, not silently fall back to env vars.
- `GET /api/secrets` returns names only; no endpoint returns a plaintext secret
  value.

---

## Out of Scope

- UI for managing secrets (names visible in schedule config; management via API
  only for now)
- Secret rotation / versioning
- Audit log for secret access (separate from run audit log)
- Image signing / SBOM (F13) — addressed in a separate spec
