# Run Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist per-run notification delivery records and surface them on the Run-detail "Notifications sent" card.

**Architecture:** New `run_notification` table written transactionally with finalize. Runner sends a redacted delivery list as part of the existing `POST /internal/runs/{id}/finalize` request. New `GET /api/runs/{id}/notifications` endpoint serves the records org-scoped.

**Tech Stack:** Go (sqlc, pgx, goose migrations), Postgres, TypeScript/React.

**Spec:** `docs/superpowers/specs/2026-05-02-run-notifications-design.md`

---

## File Structure

- Create: `internal/db/migrations/20260502000001_run_notification.sql`
- Create: `internal/db/queries/run_notification.sql`
- Regenerate: `internal/db/gen/*` via sqlc
- Create: `internal/webapi/run_notifications.go` — list handler + DTO
- Create: `internal/webapi/run_notifications_test.go`
- Modify: `internal/webapi/server.go` — register route
- Modify: `internal/api/finalize.go` — accept + persist `notifications`
- Modify: `internal/api/finalize_test.go` — coverage
- Modify: `cmd/cronfoundry/runner.go` — extend `finalizeRequest`, build payload
- Modify: `cmd/cronfoundry/runner_test.go`
- Create: `internal/redact/target.go` — `Target(kind, raw string) string`
- Create: `internal/redact/target_test.go`
- Modify: `web/src/lib/api.ts` — `runs.notifications(id)`
- Modify: `web/src/lib/types.ts` — `RunNotification` type
- Modify: `web/src/pages/RunDetail.tsx` — render the card

---

### Task 1: Migration

**Files:**
- Create: `internal/db/migrations/20260502000001_run_notification.sql`

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
CREATE TABLE run_notification (
    id          bigserial   PRIMARY KEY,
    run_id      uuid        NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    org_id      uuid        NOT NULL,
    kind        text        NOT NULL,
    target      text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('sent','skipped','failed')),
    reason      text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_notification_run_idx ON run_notification (run_id, id);
CREATE INDEX run_notification_org_idx ON run_notification (org_id, created_at DESC);

-- +goose Down
DROP TABLE run_notification;
```

- [ ] **Step 2: Apply locally**

Run: `make migrate-up` (or whatever the project Makefile uses; check `Makefile` for the actual target).
Expected: migration applies cleanly.

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/20260502000001_run_notification.sql
git commit -m "feat(db): add run_notification table"
```

---

### Task 2: SQL queries + sqlc regen

**Files:**
- Create: `internal/db/queries/run_notification.sql`
- Regenerate: `internal/db/gen/`

- [ ] **Step 1: Write queries**

`internal/db/queries/run_notification.sql`:

```sql
-- name: InsertRunNotification :exec
INSERT INTO run_notification (run_id, org_id, kind, target, status, reason)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListRunNotifications :many
SELECT id, run_id, kind, target, status, reason, created_at
FROM run_notification
WHERE run_id = $1
ORDER BY id ASC;
```

- [ ] **Step 2: Regenerate**

Run: `make sqlc` (or whatever target the project uses; check `Makefile`/`sqlc.yaml`).
Expected: generated files updated under `internal/db/gen/`.

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/db/queries/run_notification.sql internal/db/gen/
git commit -m "feat(db): InsertRunNotification + ListRunNotifications queries"
```

---

### Task 3: Redaction helper for delivery targets

**Files:**
- Create: `internal/redact/target.go`
- Create: `internal/redact/target_test.go`

- [ ] **Step 1: Write the failing test**

```go
package redact

import "testing"

func TestTarget(t *testing.T) {
    cases := []struct {
        kind, raw, want string
    }{
        {"slack", "https://hooks.slack.com/services/T0/B0/secret", "hooks.slack.com"},
        {"discord", "https://discord.com/api/webhooks/123/secret", "discord.com"},
        {"teams", "https://outlook.office.com/webhook/xyz", "outlook.office.com"},
        {"github-issue", "https://api.github.com/repos/org/repo/issues", "org/repo"},
        {"slack", "#alerts", "#alerts"},
        {"email", "team@example.com", "team@example.com"},
        {"unknown", "anything", "anything"},
    }
    for _, c := range cases {
        if got := Target(c.kind, c.raw); got != c.want {
            t.Errorf("Target(%q,%q) = %q, want %q", c.kind, c.raw, got, c.want)
        }
    }
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/redact/ -run TestTarget`
Expected: FAIL — undefined `Target`.

- [ ] **Step 3: Implement**

`internal/redact/target.go`:

```go
package redact

import (
    "net/url"
    "regexp"
    "strings"
)

// Target returns a sanitized, human-readable identifier for a publish
// destination, suitable for storage and display. Webhook URLs collapse
// to their host. github-issue URLs collapse to "<owner>/<repo>".
// Channel-style or email targets pass through. Inputs that can't be
// classified pass through unchanged.
func Target(kind, raw string) string {
    raw = strings.TrimSpace(raw)
    switch kind {
    case "slack", "discord", "teams", "http-json":
        if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
            if u, err := url.Parse(raw); err == nil && u.Host != "" {
                return u.Host
            }
        }
        return raw
    case "github-issue":
        if m := ghRepoRE.FindStringSubmatch(raw); m != nil {
            return m[1] + "/" + m[2]
        }
        return raw
    default:
        return raw
    }
}

var ghRepoRE = regexp.MustCompile(`github\.com/(?:repos/)?([^/]+)/([^/]+)`)
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/redact/ -run TestTarget`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/redact/target.go internal/redact/target_test.go
git commit -m "feat(redact): Target() — human-readable, secret-free destination labels"
```

---

### Task 4: Extend finalize body and persist notifications

**Files:**
- Modify: `internal/api/finalize.go`
- Modify: `internal/api/finalize_test.go`

- [ ] **Step 1: Extend `finalizeBody`**

In `internal/api/finalize.go`, in the `finalizeBody` struct, add:

```go
Notifications []finalizeNotification `json:"notifications,omitempty"`
```

And below the struct:

```go
type finalizeNotification struct {
    Kind   string  `json:"kind"`
    Target string  `json:"target"`
    Status string  `json:"status"`  // sent | skipped | failed
    Reason *string `json:"reason,omitempty"`
}

var validNotificationStatus = map[string]bool{
    "sent": true, "skipped": true, "failed": true,
}
```

- [ ] **Step 2: Validate**

After the existing accounting validations, add:

```go
for i, n := range body.Notifications {
    if n.Kind == "" || len(n.Kind) > 200 {
        http.Error(w, fmt.Sprintf("notifications[%d].kind: required, max 200 chars", i), http.StatusBadRequest)
        return
    }
    if n.Target == "" || len(n.Target) > 200 {
        http.Error(w, fmt.Sprintf("notifications[%d].target: required, max 200 chars", i), http.StatusBadRequest)
        return
    }
    if !validNotificationStatus[n.Status] {
        http.Error(w, fmt.Sprintf("notifications[%d].status: invalid", i), http.StatusBadRequest)
        return
    }
    if n.Reason != nil && len(*n.Reason) > 2000 {
        http.Error(w, fmt.Sprintf("notifications[%d].reason: max 2000 chars", i), http.StatusBadRequest)
        return
    }
}
```

- [ ] **Step 3: Wrap finalize + inserts in a transaction**

Replace the section that runs `q.FinalizeRun(...)` with a transactional block:

```go
tx, err := h.deps.Pool.Begin(r.Context())
if err != nil {
    http.Error(w, "begin tx", http.StatusInternalServerError); return
}
defer tx.Rollback(r.Context())
qx := dbgen.New(tx)

row, err := qx.FinalizeRun(r.Context(), dbgen.FinalizeRunParams{
    // ...same params as before...
})
if err != nil {
    // ...same handling as before...
    return
}

for _, n := range body.Notifications {
    if err := qx.InsertRunNotification(r.Context(), dbgen.InsertRunNotificationParams{
        RunID:  row.ID,
        OrgID:  row.OrgID,
        Kind:   n.Kind,
        Target: n.Target,
        Status: n.Status,
        Reason: n.Reason,
    }); err != nil {
        http.Error(w, "insert notification: "+err.Error(), http.StatusInternalServerError)
        return
    }
}

if err := tx.Commit(r.Context()); err != nil {
    http.Error(w, "commit: "+err.Error(), http.StatusInternalServerError)
    return
}
```

If `Pool` isn't already on `Deps`, add it (`*pgxpool.Pool`). The existing `q := dbgen.New(h.deps.Pool)` line tells you the pool is already accessible — just use it.

- [ ] **Step 4: Tests**

Add to `internal/api/finalize_test.go`:

```go
func TestFinalize_PersistsNotifications(t *testing.T) {
    // Build a finalize body with two notifications (one sent, one failed),
    // POST it, then SELECT FROM run_notification WHERE run_id=... and
    // assert two rows with the expected kind/target/status/reason.
    // Use the existing test harness pattern in this file.
}

func TestFinalize_RejectsInvalidNotificationStatus(t *testing.T) {
    // POST a notification with status="bogus", expect 400.
}

func TestFinalize_RollsBackOnFailedInsert(t *testing.T) {
    // Skipped if the test infra makes this hard to provoke; otherwise
    // pass an oversized reason to trip validation and verify run row
    // was not finalized (status remains pending).
}
```

(Fill in the bodies using whatever fixtures the file already uses for finalize tests.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/api/ -run TestFinalize -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/finalize.go internal/api/finalize_test.go
git commit -m "feat(api): persist notifications during finalize (transactional)"
```

---

### Task 5: List endpoint + handler tests

**Files:**
- Create: `internal/webapi/run_notifications.go`
- Create: `internal/webapi/run_notifications_test.go`
- Modify: `internal/webapi/server.go`

- [ ] **Step 1: Handler + DTO**

`internal/webapi/run_notifications.go`:

```go
package webapi

import (
    "net/http"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"

    dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type runNotificationDTO struct {
    ID        int64   `json:"id"`
    RunID     string  `json:"run_id"`
    Kind      string  `json:"kind"`
    Target    string  `json:"target"`
    Status    string  `json:"status"`
    Reason    *string `json:"reason"`
    CreatedAt string  `json:"created_at"`
}

type runNotificationsHandler struct{ deps Deps }

func (h *runNotificationsHandler) list(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(r.PathValue("id"))
    if err != nil {
        writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request"); return
    }

    // Org-scope: verify run belongs to caller's org. Use the same helper
    // events.go uses (look for the run-fetch + org-check pattern there).
    runRow, err := h.deps.Queries.GetRun(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
    if err != nil {
        writeErr(w, http.StatusNotFound, "run not found", "not_found"); return
    }
    if !sameOrg(r, runRow.OrgID) {  // use the existing org-comparison helper; rename if different
        writeErr(w, http.StatusNotFound, "run not found", "not_found"); return
    }

    rows, err := h.deps.Queries.ListRunNotifications(r.Context(), pgtype.UUID{Bytes: id, Valid: true})
    if err != nil {
        writeErr(w, http.StatusInternalServerError, "list notifications", "internal"); return
    }
    out := make([]runNotificationDTO, len(rows))
    for i, n := range rows {
        out[i] = runNotificationDTO{
            ID: n.ID, RunID: uuidString(n.RunID),
            Kind: n.Kind, Target: n.Target, Status: n.Status,
            Reason: n.Reason, CreatedAt: toISO(n.CreatedAt),
        }
    }
    writeJSON(w, http.StatusOK, out)
}
```

(If `sameOrg` / `GetRun` helpers don't exist verbatim, look at how `events.go` enforces org scope and copy that exact pattern. Don't invent new helpers.)

- [ ] **Step 2: Register the route**

In `internal/webapi/server.go`, near the existing `GET /api/runs/{id}/events` registration:

```go
nh := &runNotificationsHandler{deps: deps}
mux.Handle("GET /api/runs/{id}/notifications", session(http.HandlerFunc(nh.list)))
```

- [ ] **Step 3: Tests**

`internal/webapi/run_notifications_test.go`: org isolation (404 for run in another org), ordering (rows return in insertion order), empty case (run with no notifications returns `[]`, not `null`). Mirror the structure of the existing `events_test.go` if present, or `audit_test.go`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/webapi/ -run RunNotification -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/run_notifications.go internal/webapi/run_notifications_test.go internal/webapi/server.go
git commit -m "feat(webapi): GET /api/runs/{id}/notifications"
```

---

### Task 6: Runner — send notifications on finalize

**Files:**
- Modify: `cmd/cronfoundry/runner.go`
- Modify: `cmd/cronfoundry/runner_test.go`

- [ ] **Step 1: Extend `finalizeRequest`**

In `cmd/cronfoundry/runner.go`:

```go
type finalizeNotification struct {
    Kind   string  `json:"kind"`
    Target string  `json:"target"`
    Status string  `json:"status"`
    Reason *string `json:"reason,omitempty"`
}

type finalizeRequest struct {
    Status             string  `json:"status"`
    DurationMs         *int32  `json:"duration_ms,omitempty"`
    TokensIn           *int32  `json:"tokens_in,omitempty"`
    TokensOut          *int32  `json:"tokens_out,omitempty"`
    CostCents          *int32  `json:"cost_cents"`
    ErrorKind          *string `json:"error_kind,omitempty"`
    ErrorMsg           *string `json:"error_msg,omitempty"`
    WritebackCommitSha *string `json:"writeback_commit_sha,omitempty"`
    Notifications      []finalizeNotification `json:"notifications,omitempty"`
}
```

- [ ] **Step 2: Build the notifications slice from `result.PublishResults`**

Right before `if err := client.PostFinalize(...)`:

```go
for _, pr := range result.PublishResults {
    n := finalizeNotification{
        Kind:   pr.Type,
        Target: redact.Target(pr.Type, destinationDisplay(pr)), // see helper below
    }
    switch {
    case pr.OK && !pr.Skipped:
        n.Status = "sent"
    case pr.OK && pr.Skipped:
        n.Status = "skipped"
        if pr.SkipReason != "" { n.Reason = &pr.SkipReason }
    default:
        n.Status = "failed"
        if pr.Err != nil {
            msg := pr.Err.Error()
            n.Reason = &msg
        }
    }
    body.Notifications = append(body.Notifications, n)
}
```

`destinationDisplay(pr publish.Result) string` returns the raw target the publisher knows about (channel name, webhook URL, repo path). `publish.Result` does not currently expose this — extend `publish.Result` with a `Target string` field set by each publisher (`internal/publish/slack.go`, `discord.go`, `teams.go`, `github_issue.go`) and read `pr.Target` here. That's a small one-line-per-publisher change; do it as part of this step.

- [ ] **Step 3: Tests**

Add to `runner_test.go`: a test that intercepts the POST to `/internal/runs/.../finalize` and asserts the JSON body includes the expected `notifications` array given a stubbed `PublishResults`. The file already has the `mux.HandleFunc("/internal/runs/run-1/finalize", ...)` pattern (see line 202 area) — extend that case to capture and verify the body.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/cronfoundry/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/cronfoundry/runner.go cmd/cronfoundry/runner_test.go internal/publish/
git commit -m "feat(runner): send delivery records on finalize"
```

---

### Task 7: Frontend — types, API, card

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/RunDetail.tsx`

- [ ] **Step 1: Type**

Append to `web/src/lib/types.ts`:

```ts
export type RunNotification = {
  id:         number
  run_id:     string
  kind:       string
  target:     string
  status:     'sent' | 'skipped' | 'failed'
  reason:     string | null
  created_at: string
}
```

- [ ] **Step 2: API client**

In `web/src/lib/api.ts`, in the `runs` section:

```ts
notifications: (id: string) =>
  http<RunNotification[]>(`/api/runs/${id}/notifications`),
```

- [ ] **Step 3: Card**

In RunDetail, add a query and render:

```tsx
const isTerminal = run.status === 'succeeded' || run.status === 'failed' || run.status === 'partial_failure'
const notifsQ = useQuery({
  queryKey: ['run', run.id, 'notifications'],
  queryFn:  () => api.runs.notifications(run.id),
  enabled:  isTerminal,
})

{isTerminal && (
  <Card>
    <Card.Header>Notifications sent</Card.Header>
    <Card.Body>
      {(notifsQ.data ?? []).length === 0 ? (
        <p className="text-ink-3">No destinations configured for this run.</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[12px]">
          {notifsQ.data!.map(n => (
            <li key={n.id} className="flex flex-col gap-0.5">
              <div className="flex items-center gap-2">
                <Pill variant={n.status === 'sent' ? 'ok' : n.status === 'skipped' ? 'skip' : 'fail'}>
                  {n.status}
                </Pill>
                <span className="font-mono">{n.kind}</span>
                <span className="ml-auto font-mono text-ink-3">{n.target}</span>
              </div>
              {n.reason && (
                <span className="italic text-ink-3">{n.reason}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card.Body>
  </Card>
)}
```

(Match existing token/component conventions on the page.)

- [ ] **Step 4: Build**

Run: `cd web && npm run build`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/
git commit -m "feat(web): notifications-sent card on run detail"
```

---

### Task 8: End-to-end sanity

- [ ] **Step 1**: `go test ./...`
- [ ] **Step 2**: `cd web && npx vitest run && npm run build`
- [ ] **Step 3**: Manual smoke — fire a run with a slack destination, open Run-detail, verify the card.
