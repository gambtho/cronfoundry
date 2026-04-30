**Status:** Accepted

# E2E test job in CI

## Problem

`Makefile` exposes `make e2e`, which runs the `TestE2E_*` suite under
the `e2e` build tag in `cmd/cronfoundry/`. CI never invokes it. The
scheduler → runner → publish → finalize pipeline is the entire product,
so a regression there can land on `main` without any CI signal.

## Existing coverage (audit)

The `e2e`-tagged suite in `cmd/cronfoundry/e2e_test.go` already covers
the green-path acceptance from the handoff prompt:

- `TestE2E_SuccessfulFireWithLocalClone` — boots throwaway Postgres
  via testcontainers, builds the binary, runs `admin init`, seeds DB,
  fakes Slack + OpenAI SSE, fires a schedule, asserts:
  - run terminal status is `succeeded`
  - tokens / `cost_cents` populated
  - Slack webhook hit exactly once with the expected body
  - `publish.slack.ok` run-event recorded
- `TestE2E_MCPToolLoop` — exercises Anthropic + MCP tool dispatch.
- `TestE2E_FullScheduleFire` — exercises the failure path through the
  same pipeline.

The acceptance criteria for item #10 are met by the *existing* suite;
the only gap is wiring it into CI. No new test code is needed.

## Mechanism

Add a new job `e2e` to `.github/workflows/ci.yml`:

- `runs-on: ubuntu-latest` (Docker is preinstalled — required for
  testcontainers).
- `needs: test` so unit signal lands first.
- One step that runs `make e2e`.
- 20 min timeout (the suite builds the binary + boots three Postgres
  containers and runs three full schedule fires; budget for cold cache).
- No service Postgres — `internal/testdb` boots its own via
  testcontainers.
- No external network needed — all LLM/Slack/GitHub endpoints are
  `httptest.NewServer` stubs and the git clone uses a local bare repo
  via `CRONFOUNDRY_SMOKE_CLONE_URL`.

## Documentation

Add a short "End-to-end tests" section to `README.md` under the existing
contributor / development guidance pointing at `make e2e` and the
Docker-daemon requirement.

## Out of scope

- Adding new e2e tests or fleshing out coverage.
- Multi-cloud (AKS/Fly) e2e.
- Real-LLM integration tests.
- Touching the existing unit-test job.

## Acceptance

1. `.github/workflows/ci.yml` has an `e2e` job that runs on PRs to `main`
   and on `main` pushes.
2. The job runs `make e2e` against the real `TestE2E_*` suite end-to-end
   (testcontainers Postgres, stub LLM, stub webhook receiver, local
   bare-repo clone).
3. Job is green on `main` at merge time.
4. README documents how to run the suite locally.
