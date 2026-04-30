# Item #7: Resolve the secret-resolver split-brain

## Background

The release-readiness review flagged that the codebase has two packages with
near-identical names and overlapping responsibilities:

- `internal/secrets/` — runner-local, env-based secret resolver (used by
  the standalone runner subprocess to read API keys from env vars per the
  `CRONFOUNDRY_SECRET_<NAME>` convention).
- `internal/secretstore/` — server-side persistent secret store (envelope
  encryption + Azure Key Vault wrapper). The recent `mvpplus-3` change
  (commit `3a5fde1`) added a "pluggable secret backends" abstraction here.

A third concept lives across them: the per-run "scoped secret manifest"
(PRD FR-6.4) which is audit-logged today and may become cryptographically
enforced later.

The names are too similar; the contracts aren't documented in one place;
new contributors will guess wrong. `internal/secretstore/envelope.go` has
six `return nil, nil, err` patterns (the `(value, version, err)` triple —
fine, but the wider package has DEK envelope encryption AND a backend
abstraction interleaved, which suggests the layering wants explicit
separation.

## Goal

Make the secret subsystem's role obvious to anyone reading the code, with a
single canonical place that names the trust boundary, the backends, and the
per-run scoping.

## How to start

1. Open this worktree:
   ```bash
   cd /home/tng/workspace/cronfoundry
   git worktree add .claude/worktrees/spec-secrets-rename -b worktree-spec-secrets-rename main
   cd .claude/worktrees/spec-secrets-rename
   ```
2. Read `00-context.md` for project conventions.
3. Read the existing packages:
   - `internal/secrets/` — `resolver.go`, env-based reader
   - `internal/secretstore/` — `envelope.go`, backend interface, register/list
   - `internal/secretstore/azure/` — Azure KV implementation
   - `cmd/runner/` — consumer of `internal/secrets`
   - `cmd/cronfoundry/serve.go` — consumer of `internal/secretstore`
   - PRD FR-6 series for the scoped-manifest requirement
4. Brainstorm the target shape.

## Brainstorm questions

1. **Target naming.** Three candidates discussed in the review:
   - **`secrets/resolver` + `secrets/store/{env,azurekv}`** — caller-facing
     resolver + backend implementations under one roof.
   - **`secrets/runner` + `secrets/server`** — by consumer/role (clearer
     trust boundary, slightly more file movement).
   - **Leave names; add `secrets/doc.go`** — if the rename is too
     intrusive, just write the package boundary doc that's missing today.
   The user will pick.
2. **Envelope encryption — decorator or backend feature?** Today
   `secretstore/envelope.go` implements the DEK/envelope crypto inline.
   Cleaner: `secrets.NewEncrypted(inner Store, masterKey)` decorates any
   Store. Worth doing as part of the rename or save for a follow-up?
3. **Scoped per-run manifest — own package or method on Store?** Today
   it's audit-logged via `audit` events on the run timeline. The
   cryptographic-enforcement future is deferred to "KV-proxy sidecar." For
   this rename, do we just document the contract, or move the
   manifest-validation code into the new package?
4. **Migration path.** If we rename packages, callers move. The riskiest
   call sites: `cmd/runner` (the runner binary), `cmd/cronfoundry/serve.go`
   (server startup). Is this rename-and-go, or rename-with-deprecation
   shims for a release?

## What to deliver

Standard flow:

1. Spec → `docs/superpowers/specs/2026-04-29-secrets-layering-design.md`
   with: target package layout, contracts, trust-boundary doc, file moves.
2. Plan → `docs/superpowers/plans/2026-04-29-secrets-layering.md` —
   probably a sequence of mechanical moves + one or two refactors.
3. PR titled `refactor(secrets): consolidate secrets/ and secretstore/`.

## Acceptance

1. The new package layout has a clear `doc.go` (or `README.md`) at the
   secrets package root that explains: what each subpackage does, who calls
   what, where the master key lives, what the runner is allowed to read.
2. No new functionality (this is purely a layering / rename).
3. All existing tests still pass without modification beyond import
   path updates.
4. `go vet ./...` clean; `make check`-equivalent (`go vet`,
   `golangci-lint`) clean.
5. The two packages no longer have near-identical names.
6. The PR body includes a brief diff summary: which symbols moved, which
   stayed, which renamed.

## Out of scope

- Cryptographic enforcement of per-run secret manifest (KV-proxy sidecar
  is deferred — explicit non-goal).
- New backends (HashiCorp Vault, GCP Secret Manager, AWS Secrets Manager).
- Changing the per-secret env-var prefix convention.

## Risk to flag

This is the most likely PR to conflict mechanically with other in-flight
work because it touches widely-imported types. Sequence it:

- After items #5 and #10 land if those touch the runner / scheduler.
- Before any new feature work that adds secrets-aware code.
