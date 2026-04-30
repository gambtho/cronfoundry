**Status:** Proposed
**Date:** 2026-04-29
**Author:** release-readiness review (item #7)

# Secrets Layering Refactor

## Problem

The repository has two packages with near-identical names and overlapping
responsibilities:

- `internal/secrets/` — runner-local, env-based resolver. The standalone
  `cronfoundry-runner` subprocess uses it to read API keys from
  `CRONFOUNDRY_SECRET_<NAME>` env vars exported by the scheduler before
  exec.
- `internal/secretstore/` — server-side persistent store. Implements
  envelope encryption (DEK wrapped under a master key), backed by Postgres
  today and Azure Key Vault under `internal/secretstore/azure/`.

A new contributor cannot tell from the package names which one to import,
where the master key lives, or which side of the trust boundary each
piece sits on. There is no single document describing the secret subsystem
contracts.

## Goal

Make the secret subsystem's role obvious to anyone reading the code, with
a single canonical place that names the trust boundary, the backends, and
the per-run scoping. **No behavior change.**

## Mechanism

Consolidate both packages under one root, split by consumer/role:

```
internal/secrets/
  doc.go                          (NEW)  package-level trust-boundary doc
  runner/                         (was internal/secrets/)
    resolver.go                          package runner
    resolver_test.go
  server/                         (was internal/secretstore/)
    secretstore.go                       package server (SecretStore, ErrNotFound)
    envelope.go                          DEK envelope crypto (inline, no decorator)
    envelope_test.go
    postgres.go                          EnvelopePostgresStore
    postgres_test.go
    azurekv/                      (was internal/secretstore/azure/)
      keyvault.go                        package azurekv (KeyVaultStore)
      kvclient.go
      kvclient_real.go
      keyvault_test.go
```

Package names: `runner`, `server`, `azurekv`. Imports use
`secrets/runner` and `secrets/server` paths; the package name `runner`
collides with `internal/runner` only at import time, so consumers that
need both alias one (`runnersecrets "github.com/.../internal/secrets/runner"`).

### Symbol mapping (no rename of identifiers)

| Old | New |
|---|---|
| `secrets.Resolver` | `runner.Resolver` |
| `secrets.New` | `runner.New` |
| `secretstore.SecretStore` | `server.SecretStore` |
| `secretstore.ErrNotFound` | `server.ErrNotFound` |
| `secretstore.EnvelopePostgresStore` | `server.EnvelopePostgresStore` |
| `secretstore.NewEnvelopePostgresStore` | `server.NewEnvelopePostgresStore` |
| `secretstore.GenerateMasterKey` | `server.GenerateMasterKey` |
| `secretstore.ParseMasterKey` | `server.ParseMasterKey` |
| `secretstoreazure.KeyVaultStore` | `azurekv.KeyVaultStore` |
| `secretstoreazure.NewKeyVaultStore` | `azurekv.NewKeyVaultStore` |
| `secretstoreazure.KVClient` | `azurekv.KVClient` |
| `secretstoreazure.ErrInvalidSecretName` | `azurekv.ErrInvalidSecretName` |
| `secretstoreazure.ErrSecretNotFound` | `azurekv.ErrSecretNotFound` |

No identifier is renamed. Only the package qualifier changes.

### Trust-boundary doc (`internal/secrets/doc.go`)

A package-level comment describing:

1. **The two halves.** `runner` is loaded by the runner subprocess and
   only ever sees plaintext env vars exported for that single run.
   `server` is loaded by the cronfoundry server binary and is the only
   component that holds the master key and talks to persistent storage.
2. **Master key.** Lives in `CRONFOUNDRY_MASTER_KEY` (base64, 32 bytes
   after decode). Read once at server startup by `cmd/cronfoundry/serve.go`
   via `server.ParseMasterKey`. Never read by the runner.
3. **Per-run scoped manifest (PRD FR-6.4).** When the server prepares a
   run, it resolves only the secrets named in the skill's manifest and
   exports them as `CRONFOUNDRY_SECRET_<NAME>` env vars. The runner has
   no direct access to the store. Audit-logged today; cryptographic
   enforcement (KV-proxy sidecar) is deferred.
4. **Backends.** `server.EnvelopePostgresStore` for self-hosted Postgres;
   `azurekv.KeyVaultStore` for Azure Key Vault. Selected at startup in
   `cmd/cronfoundry/serve.go` from `CRONFOUNDRY_SECRETS_BACKEND`.

## Affected files

Importers updated (paths and aliases only):

- `cmd/runner/main.go`
- `cmd/cronfoundry/runner.go`
- `cmd/cronfoundry/runner_test.go`
- `cmd/cronfoundry/admin_init.go`
- `cmd/cronfoundry/admin_init_test.go`
- `cmd/cronfoundry/admin_setsecret.go`
- `cmd/cronfoundry/serve.go`
- `cmd/cronfoundry/serve_test.go`
- `cmd/cronfoundry/e2e_test.go`
- `internal/api/server.go`
- `internal/api/secrets.go`
- `internal/api/secrets_test.go`
- `internal/webapi/server.go`
- `internal/webapi/copilot_token.go`
- `internal/webapi/secrets_test.go`
- `internal/runner/runner.go`
- `internal/runner/runner_test.go`

Files moved (with `git mv` to preserve blame):

- `internal/secrets/*.go` → `internal/secrets/runner/*.go`
- `internal/secretstore/*.go` (non-azure) → `internal/secrets/server/*.go`
- `internal/secretstore/azure/*.go` → `internal/secrets/server/azurekv/*.go`

Package declaration updated in each moved file. The cross-package import
inside `azurekv` (today imports `secretstore` for the interface assertion)
becomes an import of `secrets/server`.

## Tests

No new tests. All existing tests must pass with import-path updates only:

- `internal/secrets/runner/resolver_test.go` (was `internal/secrets/`)
- `internal/secrets/server/envelope_test.go`
- `internal/secrets/server/postgres_test.go`
- `internal/secrets/server/azurekv/keyvault_test.go`
- All consumer tests under `cmd/cronfoundry/`, `internal/api/`,
  `internal/webapi/`, `internal/runner/`.

Acceptance command sequence:

```
go vet ./...
go test ./...
golangci-lint run ./...   # if available locally
```

## Operational notes

- No env-var changes. `CRONFOUNDRY_MASTER_KEY`, `CRONFOUNDRY_SECRETS_BACKEND`,
  and `CRONFOUNDRY_SECRET_<NAME>` keep their meanings.
- No DB migrations.
- No API changes. The internal HTTP path `/internal/secrets` is unchanged.
- Single-replica deploy; rolling forward is safe.

## Out of scope

- Cryptographic enforcement of the per-run secret manifest (KV-proxy
  sidecar) — deferred.
- Extracting envelope encryption into a `Store` decorator — deferred.
- New backends (Vault, GCP SM, AWS SM).
- Changing `CRONFOUNDRY_SECRET_<NAME>` prefix.
- Deprecation shims at old import paths — single-tenant in-repo callers
  only; rename-and-go.

## Acceptance criteria

1. `internal/secrets/doc.go` exists and documents the trust boundary,
   master-key location, scoped-manifest contract, and backend selection.
2. No file under `internal/secretstore/` (top level or `azure/`) and no
   file under `internal/secrets/*.go` outside `runner/`/`server/`.
3. `go vet ./...` and `go test ./...` pass without modifying any test
   logic; only import paths and package qualifiers change.
4. PR body includes a diff summary of which symbols moved and which
   stayed, plus the alias mapping for the rare consumer that imports both
   `internal/runner` and `internal/secrets/runner`.
5. The two packages no longer share near-identical names.
