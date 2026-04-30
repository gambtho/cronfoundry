# Secrets Layering Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate `internal/secrets/` and `internal/secretstore/` into a single `internal/secrets/{runner,server,server/azurekv}` tree, with a trust-boundary `doc.go`. Pure rename — no behavior change.

**Architecture:** Move files with `git mv` to preserve blame. Rename only the package qualifiers; identifier names are unchanged. Update every importer in one task per logical group, run `go vet` + `go test ./...` after each, commit per task.

**Tech Stack:** Go 1.25 module `github.com/gambtho/cronfoundry`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-29-secrets-layering-design.md`

**Working directory:** `/home/tng/workspace/cronfoundry/.claude/worktrees/spec-secrets-rename`. All `git`/`go` commands run there. The branch is `worktree-spec-secrets-rename`.

---

## Pre-flight

- [ ] **Verify clean baseline.** Run from the worktree:
  ```
  go vet ./...
  go test -short ./...
  ```
  Expected: both pass. If they don't, stop and report — the rename will not be doing it.

---

## Task 1: Move `internal/secrets` → `internal/secrets/runner`

**Files:**
- Move: `internal/secrets/resolver.go` → `internal/secrets/runner/resolver.go`
- Move: `internal/secrets/resolver_test.go` → `internal/secrets/runner/resolver_test.go`
- Modify importers (3 files): `cmd/runner/main.go`, `internal/runner/runner.go`, `internal/runner/runner_test.go`, plus `cmd/cronfoundry/runner.go` (uses the import) — confirm with grep.

- [ ] **Step 1: Move the files with git mv.**

  ```
  mkdir -p internal/secrets/runner
  git mv internal/secrets/resolver.go internal/secrets/runner/resolver.go
  git mv internal/secrets/resolver_test.go internal/secrets/runner/resolver_test.go
  rmdir internal/secrets 2>/dev/null || true
  ```

  Note: the `internal/secrets/` directory will be re-created in Task 3 to hold `doc.go`. The `rmdir` is a best-effort cleanup; if it fails because the dir is non-empty (it shouldn't be), inspect and fix.

- [ ] **Step 2: Change package declaration.**

  In both moved files, change the first non-comment package line:
  ```
  package secrets
  ```
  to
  ```
  package runner
  ```

  Also update the package-level doc comment in `internal/secrets/runner/resolver.go` from
  ```
  // Package secrets resolves skill-declared secrets ...
  ```
  to
  ```
  // Package runner resolves skill-declared secrets ...
  ```

- [ ] **Step 3: Update importers.**

  Find every file importing the old path:
  ```
  grep -rln '"github.com/gambtho/cronfoundry/internal/secrets"' --include='*.go'
  ```
  Expected matches (verify by running): `cmd/runner/main.go`, `cmd/cronfoundry/runner.go`, `internal/runner/runner.go`, `internal/runner/runner_test.go`.

  In each file:
  - Replace the import line:
    ```
    "github.com/gambtho/cronfoundry/internal/secrets"
    ```
    with
    ```
    runnersecrets "github.com/gambtho/cronfoundry/internal/secrets/runner"
    ```
    Use the alias `runnersecrets` because `internal/runner` already exists as a package and would collide otherwise.
  - Replace every `secrets.` qualifier in that file with `runnersecrets.` (e.g., `secrets.Resolver` → `runnersecrets.Resolver`, `secrets.New(` → `runnersecrets.New(`).

  Note: `internal/api/secrets.go` and the URL path `/internal/secrets` are NOT this package — they're an HTTP route. Do not touch them.

- [ ] **Step 4: Verify build and tests.**

  ```
  go vet ./...
  go test -short ./...
  ```
  Expected: both pass.

- [ ] **Step 5: Commit.**

  ```
  git add -A
  git commit -m "refactor(secrets): move internal/secrets to internal/secrets/runner"
  ```

---

## Task 2: Move `internal/secretstore` → `internal/secrets/server`

**Files:**
- Move (5 files): `internal/secretstore/{secretstore,envelope,envelope_test,postgres,postgres_test}.go` → `internal/secrets/server/`
- Modify importers: every file currently importing `internal/secretstore` (NOT `internal/secretstore/azure` — that's Task 3 of moving but its consumers are updated here).

- [ ] **Step 1: Move the files.**

  ```
  mkdir -p internal/secrets/server
  git mv internal/secretstore/secretstore.go internal/secrets/server/secretstore.go
  git mv internal/secretstore/envelope.go    internal/secrets/server/envelope.go
  git mv internal/secretstore/envelope_test.go internal/secrets/server/envelope_test.go
  git mv internal/secretstore/postgres.go    internal/secrets/server/postgres.go
  git mv internal/secretstore/postgres_test.go internal/secrets/server/postgres_test.go
  ```

- [ ] **Step 2: Change package declaration in moved files.**

  In all 5 moved files, change `package secretstore` → `package server`.

  In `internal/secrets/server/secretstore.go`, also update the package-level doc comment from
  ```
  // Package secretstore persists and retrieves secrets ...
  ```
  to
  ```
  // Package server persists and retrieves secrets ...
  ```

- [ ] **Step 3: Update the azurekv import (still at old path) of secretstore.**

  `internal/secretstore/azure/keyvault.go` currently imports `"github.com/gambtho/cronfoundry/internal/secretstore"` and references `secretstore.SecretStore` for the interface assertion at line ~33. Update:
  - Import line → `"github.com/gambtho/cronfoundry/internal/secrets/server"`
  - `secretstore.SecretStore` → `server.SecretStore`

  Also `internal/secretstore/azure/keyvault_test.go` imports `"github.com/gambtho/cronfoundry/internal/secretstore"`. Update:
  - Import → `"github.com/gambtho/cronfoundry/internal/secrets/server"`
  - All `secretstore.X` → `server.X`

- [ ] **Step 4: Update all other importers of `internal/secretstore`.**

  ```
  grep -rln '"github.com/gambtho/cronfoundry/internal/secretstore"' --include='*.go'
  ```
  Expected list:
  - `cmd/cronfoundry/admin_init.go`
  - `cmd/cronfoundry/admin_init_test.go`
  - `cmd/cronfoundry/admin_setsecret.go`
  - `cmd/cronfoundry/serve.go`
  - `cmd/cronfoundry/serve_test.go`
  - `cmd/cronfoundry/e2e_test.go`
  - `internal/api/server.go`
  - `internal/api/secrets_test.go`
  - `internal/webapi/server.go`
  - `internal/webapi/copilot_token.go`
  - `internal/webapi/secrets_test.go`

  In each file:
  - Replace import `"github.com/gambtho/cronfoundry/internal/secretstore"` → `"github.com/gambtho/cronfoundry/internal/secrets/server"`
  - Replace every `secretstore.` qualifier with `server.`

  Examples (do not invent symbols — only the qualifier changes):
  - `secretstore.SecretStore` → `server.SecretStore`
  - `secretstore.NewEnvelopePostgresStore` → `server.NewEnvelopePostgresStore`
  - `secretstore.GenerateMasterKey` → `server.GenerateMasterKey`
  - `secretstore.ParseMasterKey` → `server.ParseMasterKey`
  - `secretstore.EnvelopePostgresStore` → `server.EnvelopePostgresStore`

  Watch out: `cmd/cronfoundry/serve.go` line 30 imports the azure subpackage with alias `secretstoreazure` — leave that import alone in this task; it gets handled in Task 3.

- [ ] **Step 5: Verify build (azure package will still be at old path).**

  ```
  go vet ./...
  go test -short ./...
  ```
  Expected: pass. The azure files still live at `internal/secretstore/azure/` but now correctly import `internal/secrets/server`. The empty `internal/secretstore/` parent directory still exists holding only the `azure` subdir — that's fine until Task 3.

- [ ] **Step 6: Commit.**

  ```
  git add -A
  git commit -m "refactor(secrets): move internal/secretstore to internal/secrets/server"
  ```

---

## Task 3: Move `internal/secretstore/azure` → `internal/secrets/server/azurekv`

**Files:**
- Move (4 files): `internal/secretstore/azure/{keyvault,kvclient,kvclient_real,keyvault_test}.go` → `internal/secrets/server/azurekv/`
- Modify importers: `cmd/cronfoundry/serve.go` (the only consumer outside the package itself — verify with grep).

- [ ] **Step 1: Move the files.**

  ```
  mkdir -p internal/secrets/server/azurekv
  git mv internal/secretstore/azure/keyvault.go      internal/secrets/server/azurekv/keyvault.go
  git mv internal/secretstore/azure/kvclient.go      internal/secrets/server/azurekv/kvclient.go
  git mv internal/secretstore/azure/kvclient_real.go internal/secrets/server/azurekv/kvclient_real.go
  git mv internal/secretstore/azure/keyvault_test.go internal/secrets/server/azurekv/keyvault_test.go
  rmdir internal/secretstore/azure 2>/dev/null || true
  rmdir internal/secretstore 2>/dev/null || true
  ```

- [ ] **Step 2: Change package declaration in moved files.**

  In all 4 moved files, change `package azure` → `package azurekv`.

  In `internal/secrets/server/azurekv/keyvault_test.go`, the file currently aliases the package on import as `azuresecretstore`. Find and fix any usage internal to the test file referring to itself; since this is a `_test.go` inside the package (likely `package azurekv_test` or `package azure`), check carefully:
  ```
  head -20 internal/secrets/server/azurekv/keyvault_test.go
  ```
  - If the test declares `package azure`, change to `package azurekv`.
  - If it declares `package azure_test`, change to `package azurekv_test`.
  - Update any aliased import of the package itself if present (`azuresecretstore "..."` referring to the moved package gets the new path).

- [ ] **Step 3: Update importers.**

  ```
  grep -rln '"github.com/gambtho/cronfoundry/internal/secretstore/azure"' --include='*.go'
  ```
  Expected: `cmd/cronfoundry/serve.go` only.

  In `cmd/cronfoundry/serve.go`:
  - Replace the aliased import:
    ```
    secretstoreazure "github.com/gambtho/cronfoundry/internal/secretstore/azure"
    ```
    with
    ```
    "github.com/gambtho/cronfoundry/internal/secrets/server/azurekv"
    ```
  - Replace every `secretstoreazure.` qualifier with `azurekv.`.

- [ ] **Step 4: Verify build and tests.**

  ```
  go vet ./...
  go test -short ./...
  ```
  Expected: pass.

  Run the broader test suite (Postgres-backed) too:
  ```
  go test ./...
  ```
  Expected: pass (or `t.Skip` under no-DB conditions, same as baseline).

- [ ] **Step 5: Commit.**

  ```
  git add -A
  git commit -m "refactor(secrets): move internal/secretstore/azure to internal/secrets/server/azurekv"
  ```

---

## Task 4: Add trust-boundary `doc.go`

**Files:**
- Create: `internal/secrets/doc.go`

- [ ] **Step 1: Create the doc file.**

  Write `internal/secrets/doc.go` exactly:

  ```go
  // Package secrets is the umbrella for cronfoundry's secret subsystem.
  //
  // Two halves, separated by a trust boundary:
  //
  //   - secrets/runner is loaded by the runner subprocess. It reads
  //     CRONFOUNDRY_SECRET_<NAME> environment variables that the scheduler
  //     exported for one specific run. The runner has no access to the
  //     persistent store and never sees the master key.
  //
  //   - secrets/server is loaded by the cronfoundry server binary. It is
  //     the only component that holds the master key (CRONFOUNDRY_MASTER_KEY,
  //     base64-encoded 32 bytes) and that talks to persistent storage.
  //     SecretStore is the single external contract; implementations:
  //       - secrets/server.EnvelopePostgresStore — self-hosted Postgres
  //         with envelope encryption (DEK wrapped under the master key).
  //       - secrets/server/azurekv.KeyVaultStore — Azure Key Vault.
  //     The backend is selected at startup from CRONFOUNDRY_SECRETS_BACKEND.
  //
  // Per-run scoped manifest (PRD FR-6.4): when the server prepares a run,
  // it resolves only the secrets named in the skill manifest and exports
  // them as CRONFOUNDRY_SECRET_<NAME> env vars to the runner. The runner
  // has no direct access to the store. This contract is audit-logged
  // today; cryptographic enforcement (KV-proxy sidecar) is deferred.
  package secrets
  ```

  Note: `internal/secrets/` will exist as a directory holding only `doc.go` plus the `runner/` and `server/` subdirectories. The `package secrets` declared here has no callers — it exists purely as the doc anchor and to make `go doc github.com/gambtho/cronfoundry/internal/secrets` work.

- [ ] **Step 2: Verify build.**

  ```
  go vet ./...
  go test -short ./...
  go doc github.com/gambtho/cronfoundry/internal/secrets
  ```
  Expected: vet/test pass; `go doc` prints the trust-boundary comment.

- [ ] **Step 3: Commit.**

  ```
  git add internal/secrets/doc.go
  git commit -m "docs(secrets): add trust-boundary doc.go at internal/secrets root"
  ```

---

## Task 5: Final verification and PR

- [ ] **Step 1: Final clean build.**

  ```
  go vet ./...
  go test ./...
  ```
  Expected: pass. If `golangci-lint` is on PATH, also:
  ```
  golangci-lint run ./...
  ```

- [ ] **Step 2: Confirm no stragglers.**

  ```
  grep -rln '"github.com/gambtho/cronfoundry/internal/secrets"' --include='*.go' || true
  grep -rln '"github.com/gambtho/cronfoundry/internal/secretstore' --include='*.go' || true
  ```
  Both should print nothing.

  ```
  test ! -e internal/secretstore && echo "old top-level gone"
  test -d internal/secrets/runner && echo "runner ok"
  test -d internal/secrets/server && echo "server ok"
  test -d internal/secrets/server/azurekv && echo "azurekv ok"
  test -f internal/secrets/doc.go && echo "doc.go ok"
  ```
  Expect five "ok" lines.

- [ ] **Step 3: Push branch.**

  ```
  git push -u origin worktree-spec-secrets-rename
  ```

- [ ] **Step 4: Open PR.**

  ```
  gh pr create --title "refactor(secrets): consolidate secrets/ and secretstore/" --body "$(cat <<'EOF'
  ## Summary

  Resolves the secret-resolver split-brain (release-readiness item #7).
  Consolidates `internal/secrets/` and `internal/secretstore/` under one
  root, split by consumer/role:

  - `internal/secrets/runner` (was `internal/secrets/`)
  - `internal/secrets/server` (was `internal/secretstore/`)
  - `internal/secrets/server/azurekv` (was `internal/secretstore/azure/`)
  - `internal/secrets/doc.go` (NEW) — trust-boundary doc

  Pure rename. No behavior change. No env-var, API, or DB changes.

  ## Symbol movement

  All identifiers keep their names; only the package qualifier changes.

  | Old | New |
  |---|---|
  | `secrets.Resolver`, `secrets.New` | `runner.Resolver`, `runner.New` |
  | `secretstore.SecretStore`, `ErrNotFound` | `server.SecretStore`, `server.ErrNotFound` |
  | `secretstore.EnvelopePostgresStore` | `server.EnvelopePostgresStore` |
  | `secretstore.GenerateMasterKey`, `ParseMasterKey` | `server.GenerateMasterKey`, `server.ParseMasterKey` |
  | `azure.KeyVaultStore`, `KVClient`, `ErrInvalidSecretName`, `ErrSecretNotFound` | `azurekv.*` |

  Consumers that import both `internal/runner` and `internal/secrets/runner`
  alias the latter as `runnersecrets` to avoid the package-name collision.

  ## Operator notes

  - `CRONFOUNDRY_MASTER_KEY`, `CRONFOUNDRY_SECRETS_BACKEND`, and
    `CRONFOUNDRY_SECRET_<NAME>` are unchanged.
  - No DB migration. No API change. Single-replica rolling deploy is safe.

  ## Test plan

  - [x] `go vet ./...`
  - [x] `go test ./...`
  - [x] `go doc github.com/gambtho/cronfoundry/internal/secrets` shows the trust-boundary comment
  - [x] grep confirms no remaining imports of `internal/secrets` (top level) or `internal/secretstore`

  Spec: `docs/superpowers/specs/2026-04-29-secrets-layering-design.md`
  EOF
  )"
  ```

---

## Self-review notes

- Spec coverage: layout (Tasks 1-3), doc.go (Task 4), import updates (each task), no-behavior-change (verified by Tasks 1.4 / 2.5 / 3.4 / 5.1), PR diff summary (Task 5.4). ✓
- No placeholders. Every code change shows the exact transformation. ✓
- Type consistency: identifier names unchanged; only qualifier renames. ✓
- One edge handled inline: the `runnersecrets` alias for files that also import `internal/runner`. Spec mentions; plan applies it uniformly in Task 1.3.
