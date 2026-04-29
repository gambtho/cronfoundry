# F24 — Inject GitHub Installation Token into Runner at Dispatch Time

**Date:** 2026-04-23
**Status:** Shipped (563cf15)
**Author:** gambtho + Claude Code

## Problem

The runner container has no `GITHUB_TOKEN` env var. The `github-issue`
destination publisher falls back to an empty token and fails:

```
github-issue: no GitHub token available (set GITHUB_TOKEN)
```

Runs complete as `partial_failure` — the LLM call succeeds but
publishing and writeback are blocked. This has been the sole remaining
gap for two consecutive smoke test sessions (F24 in the findings log).

## Solution

At dispatch time, `serve` mints a short-lived GitHub installation token
(via the existing `InstallationCache`) and injects it as `GITHUB_TOKEN`
in the runner container's env vars.

## Changes

### 1. Scheduler gets access to `InstallationCache`

Add an `Installations` field to `internal/scheduler/tick.go` (the
`github.InstallationCache` already exists and is used by the clone-url,
writeback-push, and repo-sync paths). Wire it in `cmd/cronfoundry/serve.go`.

### 2. Resolve installation ID at dispatch time

The installation ID lives on `repo_connection.github_app_install_id`,
reachable via `run → schedule → skill → repo_connection`. Add a query
(or extend `ListPendingRuns` / `ListDueSchedulesWithSha`) to carry
`InstallationID` through to `dispatchArgs`.

### 3. Mint token in `dispatchRun()`

In `internal/scheduler/tick.go` `dispatchRun()`, before building the
`cloud.DispatchRequest`:

1. Call `deps.Installations.Token(ctx, args.InstallationID)` 
2. If successful, append `GITHUB_TOKEN=<token>` to the env vars
3. If it fails, log a warning and dispatch without it (graceful
   degradation — same `partial_failure` as today)

### 4. Runner passes `GITHUB_TOKEN` to the publisher

In `cmd/cronfoundry/runner.go` (HTTP-mode), the `fallbackToken` passed
to `NewGitHubIssuePublisher` is currently `""`. Change it to
`os.Getenv("GITHUB_TOKEN")` so the injected token reaches the publisher.

## What doesn't change

- **Writeback push** — already delegated server-side via
  `POST /internal/runs/{id}/writeback-push`; unaffected.
- **Clone URL** — already fetched from serve API with embedded token.
- **InstallationCache** — reused as-is; no changes to token caching.
- **Standalone runner** (`cmd/runner/main.go`) — already reads
  `GITHUB_TOKEN` from env.

## Token lifetime

GitHub installation tokens expire after 1 hour. Runner jobs have a
5-minute default timeout (`timeout_sec: 300`). No expiry risk.

## Failure mode

If `Token()` fails (app not installed, PEM misconfigured, network
error), the run dispatches without `GITHUB_TOKEN`. The publisher logs
the error and the run finishes as `partial_failure` — identical to
today's behavior. No regression.

## Testing

- Unit test: `dispatchRun` with a mock `InstallationCache` that returns
  a token → verify `GITHUB_TOKEN` appears in the dispatch env vars.
- Unit test: `dispatchRun` with a mock that returns an error → verify
  dispatch proceeds without `GITHUB_TOKEN`.
- Integration: next smoke test session should see `succeeded` instead
  of `partial_failure`.
