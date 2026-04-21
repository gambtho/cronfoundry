# P5 — Web UI — Design

**Status:** Proposed
**Date:** 2026-04-20
**Author:** gambtho (brainstormed with Claude)

## Overview

P5 adds the operator dashboard on top of the fully-functional P1–P3a backend. It is a React + Vite + TypeScript SPA served as an embedded static bundle from the existing `cronfoundry serve` binary. A new `/api/*` route namespace is added to the existing HTTP mux — parallel to the runner-facing `/internal/*` namespace — protected by the session auth layer built in P3a (`internal/webapi/`).

No SSR, no separate nginx container, no CDN. One binary, one container, same deployment footprint as today.

## Foundation (already built — P3a)

`internal/webapi/` provides:
- GitHub OAuth flow (`oauth.go`) — same GitHub App OAuth client credentials
- Signed session cookies (`session.go`) — HttpOnly + Secure + SameSite=Lax, KV-signed, 7-day idle timeout
- Role enforcement middleware (`auth.go`) — `admin` / `viewer` roles from `user` table
- `GET /api/me` endpoint (`me.go`)

P5 builds on this without modification.

## API Routes (`/api/*`)

All routes require a valid session cookie. Role requirements noted for writes.

### Reads (viewer + admin)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/me` | Current user (exists, P3a) |
| `GET` | `/api/repos` | List connected repo connections |
| `GET` | `/api/skills` | List skills with their schedules |
| `GET` | `/api/schedules` | List schedules with last-run summary |
| `GET` | `/api/runs` | Paginated run history; query params: `schedule_id`, `status`, `limit`, `before` |
| `GET` | `/api/runs/{id}` | Run detail: status, timing, cost, tokens, destination results |
| `GET` | `/api/runs/{id}/events` | Timeline of `run_event` rows |
| `GET` | `/api/runs/{id}/events/stream` | SSE stream for in-flight runs |
| `GET` | `/api/secrets` | Secret metadata only — name, last-updated, last-used; never values |

### Writes (admin only)

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/schedules/{id}/pause` | Flip `enabled=false`, audit-logged |
| `POST` | `/api/schedules/{id}/resume` | Flip `enabled=true`, audit-logged |
| `POST` | `/api/schedules/{id}/run-now` | Manual trigger |
| `POST` | `/api/secrets` | Create secret — value written to secret store, never returned |
| `PUT` | `/api/secrets/{name}/rotate` | Create new secret version |
| `DELETE` | `/api/secrets/{name}` | Soft-delete |
| `POST` | `/api/repos` | Connect repo post-GitHub App installation callback |
| `DELETE` | `/api/repos/{id}` | Disconnect repo |

### Error responses

All `/api/*` handlers return structured JSON errors:

```json
{"error": "schedule not found", "code": "not_found"}
```

- 400 — validation errors
- 401 — no valid session (frontend redirects to `/login`)
- 403 — valid session, insufficient role (inline message, no redirect)
- 404 — resource not found
- 500 — internal error with safe message (no stack traces)

## Frontend Pages

Client-side routing via React Router. Component library: shadcn/ui on Radix + Tailwind. Server state: TanStack Query.

### `/` — Dashboard

Schedule cards showing:
- Last-run status badge (`succeeded` / `partial_failure` / `failed` / `running` / `never`)
- Next-fire-at countdown (schedule timezone)
- "Run now" button (admin only) and pause/resume toggle (admin only)

Summary stats row at top: runs today, failure count today, cost today.

### `/runs` — Run History

Filterable table by schedule, status, and date range. Click a row opens a run detail drawer:
- Status, timing, token counts, cost estimate
- Timeline of `run_event` rows (llm.start, publish.slack.ok, writeback.commit.ok, etc.)
- Destination results with HTTP status per destination
- Log tail panel — for in-flight runs, auto-updates via SSE; falls back to 2s polling if SSE fails

### `/repos` — Connected Repos

List of connected repos with sync status and last-synced-at timestamp. "Connect repo" button opens the GitHub App installation URL (`https://github.com/apps/{appname}/installations/new`). Post-install OAuth callback lands back on this page and auto-connects the selected installation.

### `/secrets` — Secrets

Table of secret names + metadata (last-updated, last-used). "Add secret" and "Rotate" open a modal with a single masked input. Value is never shown after creation. Delete requires a confirmation dialog.

### `/login`

GitHub OAuth entry point. Redirects to GitHub, returns to the page the user was navigating to (stored in session state before redirect).

## Static Bundle Packaging

The React bundle is built with `make web` (runs `vite build` in `/web`), output to `/web/dist`. The Go binary embeds `/web/dist` via `embed.FS` and serves it on `/*` with a catch-all that returns `index.html` for unknown paths (client-side routing). API and internal routes are registered first on the mux and take precedence.

Build order: `make web` must run before `make build`. CI runs both in sequence.

## Service Layout Additions

```
/web
  src/
    pages/          # Dashboard, Runs, Repos, Secrets, Login
    components/     # shared: RunStatusBadge, SecretModal, LogTail, etc.
    lib/            # api.ts (fetch wrappers), types.ts
  index.html
  vite.config.ts
  package.json

/internal/webapi/
  repos.go          # GET /api/repos, POST /api/repos, DELETE /api/repos/{id}
  skills.go         # GET /api/skills
  schedules.go      # GET /api/schedules, pause/resume/run-now
  runs.go           # GET /api/runs, /api/runs/{id}
  events.go         # GET /api/runs/{id}/events, /events/stream (SSE)
  secrets.go        # GET/POST/PUT/DELETE /api/secrets
  static.go         # embed.FS serving + SPA catch-all
```

Existing `internal/webapi/` files (P3a: auth, oauth, session, me) are unchanged.

## Testing

**Handler tests** follow the pattern in `internal/api/*_test.go`: table-driven, `internal/testdb` for DB-backed tests, test helpers for session injection. Each new handler file gets a corresponding `_test.go`.

**Frontend** uses Vitest for unit tests on components with non-trivial logic (SecretModal, LogTail SSE fallback, role-gated button visibility). No E2E browser tests for MVP.

## Implementation Tracks

| Track | Scope | Prerequisite |
|---|---|---|
| P5a | `/api/*` handler layer + DB queries | P3a merged |
| P5b | React app scaffolding + Dashboard + Run History pages | P5a |
| P5c | Repos + Secrets write surfaces + static bundle embed | P5b |
| P5d | SSE live-tail + polish (dark mode, responsive, toasts) | P5c |

P5a and the React scaffold can start in parallel. P5b–d are sequential.

## Success Criteria

An operator with a running CronFoundry instance can:
1. Log in via GitHub OAuth and land on the Dashboard.
2. See all schedules, their next-fire times, and last-run status without using the CLI.
3. Pause a schedule and trigger a manual run from the UI.
4. Watch a run's event timeline update live while it executes.
5. Add a new secret and rotate an existing one without the value ever appearing in the UI.
6. Connect a new GitHub repo via the App installation flow.
