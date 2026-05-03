# Run notifications — design

## Problem

The Run detail page mocks include a "Notifications sent" card listing
the slack/discord/teams/github-issue deliveries that went out for a
run, with their outcome (sent / skipped / failed) and a reason when
applicable. The runner already computes this list
(`runner.RunResult.PublishResults`) but it's discarded after finalize.

## Approach

Persist delivery records in a new `run_notification` table written
during finalize, and expose them via
`GET /runs/{id}/notifications`. This is *not* an event log — it's an
audit-style record table answering "what got sent for this run".

Rejected alternatives:

- **`run_event` rows** — events are observability-shaped (free-form
  payload, prunable); querying JSON for "did slack go out" forever
  is the wrong shape.
- **`audit_log`** — wrong scope. That table records org-level human
  and system actions on resources, not outbound delivery, and lacks
  a run FK.

## Schema

New table `run_notification`:

```sql
CREATE TABLE run_notification (
    id          bigserial   PRIMARY KEY,
    run_id      uuid        NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    org_id      uuid        NOT NULL,
    kind        text        NOT NULL,                              -- 'slack' | 'discord' | 'teams' | 'github-issue' | 'http-json' | ...
    target      text        NOT NULL,                              -- redacted, human-readable: '#alerts', 'team@example.com', 'hooks.slack.com'
    status      text        NOT NULL CHECK (status IN ('sent','skipped','failed')),
    reason      text,                                              -- skip-condition or error message; NULL on success
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_notification_run_idx ON run_notification (run_id, id);
CREATE INDEX run_notification_org_idx ON run_notification (org_id, created_at DESC);
```

`org_id` is denormalized (also reachable via `run.org_id`) to keep
list queries scoped without a join, matching the pattern of
`run_event` / `audit_log`.

`kind` is unconstrained text — publisher names already form a stable
set in `internal/publish/`, and forcing a CHECK constraint creates
a migration every time someone adds a publisher. The list endpoint
just passes the value through.

`target` is a *redacted* identifier suitable for display. Webhook
URLs collapse to host (`hooks.slack.com`); channel/email targets
pass through. The runner is responsible for redaction before
sending — the API does no further sanitization.

## Status mapping

`publish.Result` → row:

| Result fields                            | row.status | row.reason            |
| ---------------------------------------- | ---------- | --------------------- |
| `OK=true,  Skipped=false`                | `sent`     | NULL                  |
| `OK=true,  Skipped=true`                 | `skipped`  | `result.SkipReason`   |
| `OK=false`                               | `failed`   | `result.Err.Error()`  |

## Write path

Extend `POST /internal/runs/{id}/finalize` body with an optional
`notifications` array:

```json
{
  "status": "succeeded",
  "...": "...",
  "notifications": [
    {"kind":"slack","target":"#alerts","status":"sent"},
    {"kind":"discord","target":"hooks.discord.com","status":"failed","reason":"timeout"}
  ]
}
```

Validation:

- `kind` and `target` non-empty (≤ 200 chars each).
- `status` is one of the three values above.
- `reason` ≤ 2000 chars.
- The whole array is optional and may be empty (older runners, or
  schedules with no destinations).

Persistence:

- Insert all rows in the same transaction as the run's terminal
  update. Either everything lands or nothing does — a finalize that
  succeeds must reflect a complete delivery record.
- `org_id` is read from the run row (the handler already has it).
- 409 (already-finalized) short-circuits before insertion, same as
  today.

## Runner changes

`internal/runner/runner.go` already produces `[]publish.Result`. The
runner's HTTP client to the API:

1. Maps each `publish.Result` to the JSON shape above.
2. Calls `redact.Target(kind, raw)` to derive `target` from the
   resolved destination (host-only for webhooks, channel name for
   slack/discord/teams, URL for github-issue, address for email).
3. Sends them in the existing finalize request — no new endpoint
   call.

If the runner crashes before finalize, no rows are written. That's
the same gap that exists for run terminal status today; no new
class of inconsistency is introduced.

## Read path

New endpoint `GET /runs/{id}/notifications`, org-scoped (mirrors
`GET /runs/{id}/events`):

- Authn: existing org bearer.
- Authz: 404 if the run isn't in the caller's org.
- Response: `[]runNotificationDTO`, ordered by `id ASC` (insertion
  order matches dispatch order).

DTO:

```ts
type RunNotification = {
  id:         number
  run_id:     string
  kind:       string
  target:     string
  status:     'sent' | 'skipped' | 'failed'
  reason:     string | null
  created_at: string  // ISO
}
```

No SSE — a run's notifications are written once at finalize, so
polling-on-demand from the run-detail page is enough. The card
queries on mount and on run-status transitions to terminal.

## Frontend

`api.runs.notifications(id)` calls the new endpoint. The card
renders:

- One row per notification.
- Status pill: `ok` for sent, `skip` for skipped, `fail` for failed.
- `target` in mono.
- `reason` (italicized) on the second line when present.
- Empty state: "No destinations configured for this run."

The card hides while the run is non-terminal (status ∈
{`pending`,`running`}) — there's nothing to show yet.

## Backward compatibility

Old runs (pre-migration, or runs from a runner that doesn't send
the `notifications` field) have zero rows in `run_notification`.
The card shows the empty state. There's no backfill — historical
delivery records are gone and not worth reconstructing.

## Testing

- **Migration**: standard up/down test under `internal/db/`.
- **Finalize handler**: validation cases (bad status, oversized
  reason, missing field), transactional rollback on bad input
  after partial writes (use a single tx, not pre-validated then
  inserted).
- **List handler**: org isolation, ordering, empty case.
- **Runner**: redaction of webhook URLs, status mapping table.
- **Frontend**: card empty state, status-pill mapping, hidden
  while non-terminal.

## Out of scope

- Latency / response code / retry count fields. Add later if
  asked; the table can grow nullable columns without reshaping the
  contract.
- A delivery-retry mechanism — this records what happened, it
  doesn't drive new sends.
- Cross-run "notifications by destination" reporting — that's an
  org-wide query the schema supports (`org_id` index) but which no
  page currently asks for.
