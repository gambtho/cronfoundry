# Item #10: Add an end-to-end test job to CI

## Background

`Makefile` has an `e2e` target:

```make
e2e:
	go test -tags=e2e ./cmd/cronfoundry/... -count=1 -timeout 10m -run TestE2E_
```

…but `.github/workflows/ci.yml` does not invoke it. CI runs `go vet`,
`go test ./...` (with a service Postgres), and `golangci-lint`. The e2e
suite is opt-in via the `e2e` build tag and isn't gated on PRs.

The release-readiness review's finding: with the scheduler/runner/publish
pipeline being the entire product, no CI-enforced end-to-end signal means
regressions in the most user-visible path can land. The smoke-test
findings doc (`docs/guides/smoke-test-mvp-azure-findings.md`) is doing the
job CI should be doing.

This is the only mechanical item in the remaining set — no brainstorm
needed. Skip straight to a plan + execution.

## Goal

Every PR to `main` runs the end-to-end suite. A regression in the green-path
"schedule fires → runner runs → output publishes → memory writes back"
pipeline fails the build before merge.

## How to start

1. Open this worktree:
   ```bash
   cd /home/tng/workspace/cronfoundry
   git worktree add .claude/worktrees/spec-e2e-ci -b worktree-spec-e2e-ci main
   cd .claude/worktrees/spec-e2e-ci
   ```
2. Read `00-context.md` for project conventions.
3. Read what already exists:
   - `Makefile` `e2e` target
   - Search for `TestE2E_` to find current e2e tests:
     ```bash
     grep -rn "TestE2E_\|//go:build e2e\|// +build e2e" --include="*.go"
     ```
   - `.github/workflows/ci.yml`
   - Check if `testdata/skills/weekly-digest/` is the fixture used (likely)
4. Run the existing suite locally to understand requirements:
   ```bash
   make e2e
   ```
   Note what services/env vars it needs (Postgres? LLM? webhook receiver?).

## Plan-mode straight to plan

Skip brainstorming for this. Use `superpowers:writing-plans` directly. The
plan should produce these tasks (rough sketch — write detailed TDD-style
tasks):

1. **Audit existing e2e tests.** Identify what they currently exercise. If
   the existing `TestE2E_*` tests already provide good coverage, skip to
   step 3. If they're stub or partial, decide what to flesh out.
2. **(If needed) Add a green-path e2e test** that:
   - Boots Postgres (testcontainers, like other integration tests).
   - Boots a stub LLM HTTP server returning a canned completion.
   - Boots a stub webhook receiver.
   - Loads `testdata/cronfoundry.yaml` + the `weekly-digest` skill fixture.
   - Runs the runner end-to-end.
   - Asserts: the run record terminal state is `succeeded`, the stub
     webhook received the expected body, the run row's `cost_cents` is
     populated.
3. **Add a CI job.** In `.github/workflows/ci.yml`, add an `e2e` job:
   - Service: `postgres:16` (mirror the existing `test` job).
   - Step: `make e2e` with `TEST_DATABASE_URL` env set.
   - `needs: test` so it runs after the unit suite (cheap signal first).
   - Timeout: 15 minutes.
4. **Document.** A short `## E2E tests` section in the README's
   contributing area or in `docs/guides/` explaining what's covered.

## Trade-offs

- **Build time.** Adding e2e to PRs adds ~3-5 minutes per build. Acceptable
  for a serious project.
- **Flakiness risk.** Testcontainers + stub HTTP + LLM stubs are
  reasonably stable; if flakes occur, mark with `t.Retry`-style helpers
  rather than disabling.
- **External LLM.** The stub LLM is the right call for CI; never hit a
  real provider from CI.

## Acceptance

1. `.github/workflows/ci.yml` has an `e2e` job that runs on every PR to
   `main` and on `main` pushes.
2. The job exercises the full schedule → runner → publish → finalize loop
   end-to-end against a stub LLM and stub webhook receiver, with Postgres
   as the persistence layer.
3. The job passes on `main` at the time of merge.
4. A documented step (in the README or contributing guide) tells operators
   how to run the suite locally.

## Out of scope

- Adding new product features as part of this work.
- Touching the existing unit-test suite.
- Multi-cloud e2e (AKS-specific, Fly-specific). Local in-CI is enough.
- Real-LLM integration tests (those exist under the `integration` build
  tag in `internal/integration/`; explicitly out of scope for CI per the
  `/improve` skill's guidance).

## PR title

`ci: run end-to-end test suite on every PR`
