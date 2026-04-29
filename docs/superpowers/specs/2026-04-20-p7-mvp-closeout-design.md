# P7 — MVP Close-out — Design

**Status:** Shipped (f7f2f93)
**Date:** 2026-04-20
**Author:** gambtho (brainstormed with Claude)
**Depends on:** P6 (`docs/superpowers/plans/2026-04-20-p6-mvp-gaps.md`)

## Overview

P6 closes the MVP *code* gaps (push-webhook resync, secret-manifest logging, audit writes, DB-backed allowlist). This spec (P7) closes the remaining three MVP gaps that are not code-mutation work: a live-tail log UI component that was scoped into P5 but didn't land, a status/roadmap refresh across the operator-facing surfaces (README + GitHub Pages), and a once-through Azure smoke runbook that proves an honestly-done MVP end-to-end.

The design target for P7 is to mark MVP shipped with one PR, one merged branch, and one verified deployment. Nothing in P7 adds features; every item maps to a specific claim in the original spec (`2026-04-19-cronfoundry-design.md`) that isn't yet backed by shipped code or shipped docs.

## Goals

- Make "live-tail logs for in-flight runs" — listed as MVP in the original spec — actually work in the UI.
- Bring the README and `docs/index.html` roadmap/status copy in line with reality ("MVP shipped"), without touching design or marketing content.
- Produce a runbook an operator can follow top-to-bottom to deploy CronFoundry to Azure and land a real scheduled run with publish + writeback, executed once by hand as the integration test.

## Non-Goals

- No new features from the deferred list. No MCP, no Copilot provider, no auto-pause, no new destinations, no conditional routing.
- No refactors or cleanup outside what the three items above touch.
- No re-design of the GitHub Pages landing page; copy edits only.
- No automation of the Azure smoke into CI. The runbook is manual.
- No audit-log UI, push-webhook docs, or `/api/users` docs — those belong to P6's wrap-up, not here.
- No endpoint-level docs for `/webhook/github`, `/api/audit`, `/api/users`, or the `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET` env var. Those are P6's.

## Architecture

P7 is three independent workstreams that share a single PR for mergeability:

1. **`web/src/components/LogTail.tsx`** — new React component; consumes the existing `/api/runs/{id}/events/stream` SSE endpoint (no server-side change). Wired into the run detail drawer on `web/src/pages/Runs.tsx`.
2. **Docs refresh** — targeted edits to `README.md` and `docs/index.html`.
3. **`docs/guides/smoke-test-mvp-azure.md`** — new operator-facing runbook.

No new Go packages, no new DB migrations, no new endpoints.

## Components

### LogTail component

`web/src/components/LogTail.tsx` — dark, monospaced, auto-scrolling panel with two modes:

- **Streaming mode** (run status is `pending` or `running`): opens an `EventSource` on mount, closes on unmount, and auto-closes when the observed status becomes terminal (`succeeded | partial_failure | failed`).
- **Static mode** (run already finished when the component mounts): fetches recent events via the existing non-streaming `/api/runs/{id}/events` endpoint and renders them with the same row layout. No SSE.

Props:

```ts
type Props = {
  runId: string;
  status: RunStatus;  // from parent Runs.tsx
};
```

Row layout: `HH:MM:SS  <level>  <event_type>  <short_payload>`. Click a row to expand the full JSON payload.

**Auto-scroll behavior:** sticky-to-bottom by default; pauses when the user scrolls up (detected via `scrollTop + clientHeight < scrollHeight - threshold`); resumes when the user scrolls back within the threshold. Standard terminal-log pattern.

**Reconnect:** `EventSource` retries transient drops automatically using the browser's native reconnection logic (the component does not manage backoff itself). The component counts consecutive `onerror` events; after 5 it closes the stream and shows a "connection lost — reload to retry" inline message. No silent infinite reconnect.

### Runs page integration

`web/src/pages/Runs.tsx` — the detail drawer already renders run summary fields. Add `<LogTail runId={...} status={...} />` below the summary. No routing changes, no new TanStack Query keys.

### Docs refresh

**`README.md`** — targeted edits only:

- **Status** block: replace "P2 service layer complete" with "MVP shipped — deployable to Azure."
- **Roadmap** block: strike the P1–P5 bullet list; replace with a brief "Shipped" line and a link to the deferred-items section of the original design spec.
- **Quick start** block: confirm every command matches current Make targets / CLI; swap local-only framing for "Local dev harness" + a one-line "Azure deploy" pointer to the runbook.
- **Architecture** block: add the `webapi`, `secretstore`, `token` internal packages that landed after P1; remove stale references.

**`docs/index.html` (GitHub Pages)** — targeted edits only:

- Any "coming soon" / status-in-progress language → "Shipped."
- Roadmap/Status section: reflect MVP shipped; add one-line "Self-host on Azure" beat with a link to the runbook.
- Nav, hero headline, visual design, terminal animation, and meta tags: untouched.

**Other docs:** spot-check `docs/guides/deploy-azure.md`, `docs/guides/observability.md`, `docs/guides/smoke-test-p2.md`, `docs/guides/smoke-test-p4.md` for stale references and fix only if actively wrong. No re-polishing.

### Azure smoke runbook

`docs/guides/smoke-test-mvp-azure.md` — sections, in order:

1. **Prerequisites** — Azure subscription, `az` + Bicep toolchain, a GitHub account able to register an App, an OpenAI *or* Anthropic *or* Azure AI Foundry key, a Slack incoming webhook URL, a target "reports" repo.
2. **Register the GitHub App** — scopes/permissions, callback URL pattern. Link to `docs/webhook-setup.md` (owned by P6a) for the webhook half; this runbook covers the OAuth half.
3. **Deploy via Bicep** — the one `az deployment sub create -f deploy/main.bicep -p params.json` command, which parameters to set, how to find the resulting API hostname.
4. **First-boot config** — paste the GitHub App creds, set the LLM key + Slack webhook as secrets, connect the skill repo.
5. **Land a skill** — either use the bundled `testdata/` fixture or a minimal copy of `cronfoundry.yaml` + one `SKILL.md` that fires every 5 minutes.
6. **Observe the first fire** — watch the dashboard, open the run detail, confirm the `LogTail` streams.
7. **Verify the side effects** — Slack message appears, GitHub issue filed in the reports repo, `memory.md` commit lands from `cronfoundry[bot]`.
8. **Verify audit log** — open the Audit page in the web UI (P6c); confirm repo-connect, secret-create, and login events are listed for the session.
9. **Teardown** — single `az group delete` line.

**Success criteria** (checked at the end): one `succeeded` run visible in the dashboard, three expected side effects present (Slack, GitHub issue, writeback commit), audit log non-empty.

**Failure handling:** any failed step captures the cause in the doc as a fix (code or doc), then re-runs from that step. The committed runbook is the version that actually worked.

## Data flow

### LogTail — streaming mode

```text
Runs.tsx (user opens detail drawer)
  → <LogTail runId=X status=running />
    → new EventSource("/api/runs/X/events/stream")
      → server streams run_event rows as SSE
        → component appends rows, auto-scrolls
        → user scrolls up → sticky-to-bottom pauses
        → status transitions to "succeeded" via parent re-render
          → useEffect closes EventSource
```

### LogTail — static mode

```text
Runs.tsx (user opens detail drawer for finished run)
  → <LogTail runId=X status=succeeded />
    → fetch("/api/runs/X/events")
      → render all rows; no stream opened
```

## Error handling

- **SSE connection drop (transient):** `EventSource` auto-reconnects via the browser's native logic; the component counts consecutive errors and after 5 closes the stream and shows inline "connection lost — reload to retry." No silent retry loop.
- **Missing events endpoint response (static mode, network error):** empty panel with "no events recorded" placeholder. Not a crash.
- **Run finishes mid-stream:** `useEffect` on `status` prop closes the stream when status becomes terminal. No orphan connections.
- **Unmount during stream:** cleanup closes `EventSource` in the effect's teardown.
- **Smoke runbook failure at any step:** captured in the doc as a fix (code or doc), then the runbook re-runs from that step. The committed runbook is the version that actually worked.

## Testing

### LogTail — Vitest

- Subscribes to `EventSource` with the right URL on mount.
- Closes the stream on unmount.
- Auto-closes when status prop transitions to terminal.
- Sticky-to-bottom pauses when user scrolls up; resumes when user scrolls back.
- Static mode (terminal status on mount) fetches historical events without opening a stream.
- Reconnect cap surfaces the inline "connection lost" message after 5 failures.

### Backend SSE handler

Existing tests in `internal/webapi/events_test.go` stay as-is. P7 doesn't change the server side.

### Docs

No automated tests for README or `docs/index.html`. The Azure smoke run is the integration test for the doc refresh (broken claims caught during walkthrough become fixes).

### Azure smoke runbook

Executed once end-to-end by the author against a live Azure subscription. Any failing step → fix root cause (code or doc), re-run from that step, commit the version that worked.

**Explicitly out of scope:** load tests, chaos tests, e2e browser tests, CI automation of the Azure smoke.

## Implementation order

Ordered for small, independently-reviewable commits:

1. **LogTail component + Runs wire-up + Vitest.** Self-contained; merges even if docs lag.
2. **README + `docs/index.html` edits.** Pure copy; quick review.
3. **Write `smoke-test-mvp-azure.md`.** Can start after P6 lands.
4. **Execute the smoke** against a live Azure sub. Fix anything it catches. Commit the final runbook.

Steps 1–3 can land as one PR; step 4 is an ops task that produces a final commit against the runbook.

## Dependencies & sequencing

- P7 item 1 (LogTail) has **no dependency** on P6 and can ship immediately.
- P7 items 2–3 (docs, runbook) are **best written after P6 merges**, because both reference endpoints (`/api/audit`) and env vars (`CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`) that P6 introduces. Writing them against a pre-P6 tree would require a follow-up pass.
- P7 item 4 (execute smoke) **requires P6 merged** to test the audit-log verification step.

## Success criteria

An operator can, in one afternoon:

1. Deploy CronFoundry to Azure by following the runbook.
2. Land a real scheduled run with publish + writeback against a live GitHub repo.
3. See the `LogTail` stream events while the run is in flight, and see the finalized event list after it completes.
4. Open the Audit page and see every mutation they performed.
5. Read the README or the GitHub Pages site and find nothing out of date or "coming soon."

If that loop works end-to-end on the first try following only what's in the repo, MVP is shipped.
