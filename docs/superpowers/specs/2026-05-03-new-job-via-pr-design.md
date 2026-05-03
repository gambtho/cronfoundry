# Add-job / Import-job via skill-repo PR (and Run-now navigation)

## Background

`Jobs.tsx` currently renders two primary actions — `+ New job` and `Import from yaml` — that are bare `<Button>` elements with no `onClick` handler and no backing routes or endpoints. Clicks do nothing. The dashboard's `Run now` button on each job is wired but discards the new run id and stays on the schedules list, so the user has no signal that anything happened.

This spec covers three changes that ship together:

1. **`+ Add job`** — replaces `+ New job`. Form-driven; produces a PR against the connected skill repo that appends a `Schedule` entry to an existing `SkillEntry` in `cronfoundry.yaml`.
2. **`+ Import job`** — replaces `Import from yaml`. Same backend pipeline; user pastes a single-schedule YAML snippet instead of filling a form.
3. **Run-now → run page navigation** — the action returns the new run id and the UI navigates to `/runs/<id>` so the user immediately sees the run progress.

The work also adds `pull_requests:write` to the GitHub App's default permissions and surfaces a clear error path when an existing install hasn't accepted the new permission yet.

## Goals

- Operators can create a new schedule from the dashboard without leaving the UI, with the same audit-trail and PR-review semantics as a hand-edited `cronfoundry.yaml` change.
- A single backend endpoint serves both the form and the YAML-paste paths so validation and PR-creation logic live in one place.
- YAML edits preserve comments, ordering, indentation, and quoting style of the surrounding document; PR diffs only show the inserted lines.
- Run-now provides immediate, navigable feedback.

## Non-goals

- Creating a brand-new skill (`skills/<name>/SKILL.md`) from the UI. Operators add skills via PR for now.
- Optimistic concurrency between two operators editing `cronfoundry.yaml` simultaneously. We rely on GitHub merge conflicts.
- Open-PR collision detection (clicking "Add job" with the same name twice). Documented as a known limitation; sync will surface a duplicate-name validation error if both PRs land.
- A separate `+ New skill` action. Out of scope for v1.
- Round-tripping `cronfoundry.yaml` through the typed `*config.Manifest` representation (would lose comments).

## User-visible flows

### Add job (form)

1. Click `+ Add job` on `Jobs.tsx` (or press `N`). Navigate to `/jobs/new`.
2. Form, single page:
   - **Skill** (required) — dropdown of existing `path: skills/<name>` entries from the connected skill repo. Sourced from the existing `GET /api/skills` endpoint.
   - **Name** (required) — schedule name.
   - **Cron** (required).
   - **Timezone** (default `UTC`).
   - **Provider** + **Model** (required).
   - **Destinations** — repeater, one row per destination, with a type selector (`github-issue` | `slack` | `discord` | `teams` | `http` | `email`) and a `when` selector (`always` | `on_success` | `on_failure`).
   - **Writeback** — optional toggle; if enabled, surface `path` and `mode` (`append` | `replace`).
   - **Advanced** (collapsed by default) — `overlap_policy`, `timeout_sec`, `max_turns`, `copilot_prefix`, `auto_pause`, `env`, `mcp_env`.
3. Submit → `POST /api/skill-repo/jobs` with `{ skill_path, schedule }`.
4. On success, render a card showing the PR number and a "View PR" link. The card explains that the schedule will appear after the PR merges and the next sync runs (~60s after the merge push).
5. Error states:
   - **400** — surface the parser error inline at the top of the form; keep all field state.
   - **409** — the chosen skill is no longer in `cronfoundry.yaml`; refetch skills and ask the user to re-pick.
   - **412** — GitHub App is missing `pull_requests:write`. Render a card with a CTA linking to `data.review_url` (the App's permissions-review page).
   - **502** — generic "GitHub API failed; try again."

### Import job (YAML paste)

1. Click `+ Import job` on `Jobs.tsx`. Navigate to `/jobs/import`.
2. Skill dropdown (same source as Add job) + a textarea for one `Schedule` YAML object — i.e. the body of a single `schedules:` list entry, not a full manifest.
3. Client parses YAML to JSON via `js-yaml` (new frontend dep) on submit. Parse errors surface inline; the textarea retains the user's input.
4. Once parsed, the request shape is identical to Add job: `POST /api/skill-repo/jobs` with `{ skill_path, schedule }`.
5. Success and error handling are identical to Add job.

### Run-now navigation

- `Jobs.tsx` and `JobDetail.tsx`: clicking `Run now` calls `api.schedules.runNow(id)`, which now returns `{ run_id }`. The mutation's `onSuccess` invalidates the `runs` query and `useNavigate()`s to `/runs/<run_id>`.
- The existing `RunDetail` page already handles "run still pending/dispatched" — it shows pending state until the scheduler picks it up and the run-event stream begins. No new UI work there.

## Backend architecture

### New endpoint: `POST /api/skill-repo/jobs`

Admin-only, session-auth (matches `pause` / `resume` / `run-now`).

```
POST /api/skill-repo/jobs
Content-Type: application/json
{
  "skill_path": "skills/smoke",
  "schedule":   { ... config.Schedule shape, JSON-encoded ... }
}

200 OK
{ "pr_url": "https://github.com/.../pull/42", "pr_number": 42, "branch": "cronfoundry/add-job-foo-1714752000" }

400 Bad Request
{ "error": "manifest: skill 0: schedule 1: cron: invalid expression \"0 9 * * 8\"", "code": "validation" }

409 Conflict
{ "error": "skill_path 'skills/foo' not in cronfoundry.yaml on default branch", "code": "skill_not_found" }

412 Precondition Failed
{ "error": "github app missing pull_requests:write permission", "code": "permission_required",
  "review_url": "https://github.com/settings/apps/<slug>/permissions" }

502 Bad Gateway
{ "error": "github api: 500 internal server error", "code": "gateway" }
```

### Pipeline

One synchronous request, no goroutines, no retries:

1. **Auth + parse.** `adminOnly` middleware. Decode body. Reject empty `skill_path` or empty `schedule.name`.
2. **Resolve target connection.** Look up the org's `repo_connection`. If none, 400.
3. **Fetch current YAML.** Mint an installation token. `GET /repos/{owner}/{repo}/contents/cronfoundry.yaml?ref=<default_branch>` via `go-github`. Capture content bytes, file `sha`, and the head commit sha of the branch.
4. **Edit YAML.** Call `yamledit.AppendScheduleToSkill(currentYAML, skillPath, schedule)` (new package). Returns updated bytes or a typed error (`ErrSkillNotFound`, `ErrDuplicateScheduleName`).
5. **Validate after edit.** Call `config.ParseManifest(updatedYAML)`. If it errors, return 400 with the parser's message verbatim. Belt-and-suspenders: yamledit doesn't validate the manifest as a whole, so this step catches anything missed (duplicate names within the same skill, manifest-level constraints).
6. **Open the PR.** Sequence:
   1. `POST /repos/{o}/{r}/git/refs` — create branch `cronfoundry/add-job-<sanitized-schedule-name>-<unix-ts>` from the head commit sha captured in step 3. (`<sanitized-schedule-name>` lower-cases the input and replaces non-`[a-z0-9-]` runs with `-`.)
   2. `PUT /repos/{o}/{r}/contents/cronfoundry.yaml` with `branch=<new branch>`, `sha=<file sha from step 3>`, `content=<base64(updatedYAML)>`. Commit message: `chore(cronfoundry): add job <name> to <skill_path>`.
   3. `POST /repos/{o}/{r}/pulls` — open PR. Title same as commit msg. Body: a markdown-rendered summary of the schedule fields (cron, provider, model, destinations summary, writeback path if any).
   4. If the **branch creation** or **content PUT** returns 403 with `{message: "Resource not accessible by integration"}` or similar, surface as 412 with a constructed `review_url`. (Apps API doesn't reliably 403 specifically for missing `pull_requests:write` until the `pulls` POST, so we 412 there too.)
7. **Audit.** `audit.Log` entry: `action="schedule.proposed"`, `target_kind="repo_connection"`, `target_id=conn.ID`, `detail={skill_path, schedule_name, pr_url, pr_number}`. Failure to log is a `slog.Warn`, not a request failure.
8. **Return** `{pr_url, pr_number, branch}`.

If branch creation succeeds but a later step fails, the orphaned branch is left in place; `slog.Warn` records its name for manual cleanup. We don't attempt rollback in v1.

### Module layout

New packages:

- `internal/skillrepo/`
  - `client.go` — thin `Client` wrapping `go-github` for the four calls (`GetFile`, `CreateBranch`, `PutFile`, `CreatePR`). Handles install-token minting via `internal/github.InstallationCache`. Returns typed errors (e.g. `ErrPermissionRequired`, `ErrConflict`).
  - `client_test.go` — uses `httptest.Server` like `internal/publish/githubissue.go` does; tests the four operations independently.
- `internal/yamledit/`
  - `append_schedule.go` — `AppendScheduleToSkill(yamlBytes []byte, skillPath string, sched *config.Schedule) ([]byte, error)`. Implementation walks a `yaml.v3` `*yaml.Node` tree, finds the matching `path:` value in the `skills:` sequence, marshals the schedule into a `yaml.Node` with omitempty tags, appends to the target `schedules:` sequence, and re-marshals the whole tree. Comments, anchors, ordering, and source indentation are preserved by `yaml.v3`'s round-trip.
  - `append_schedule_test.go` — table-driven golden-file tests.

New file in existing package:

- `internal/webapi/skill_repo_jobs.go` — the handler.
- `internal/webapi/skill_repo_jobs_test.go` — handler test wiring `skillrepo.Client` and `yamledit` via interfaces.

Modified files:

- `internal/webapi/server.go` — register the new route.
- `internal/webapi/schedules.go` — Run-now handler now forwards `{run_id}`.
- `internal/githubapp/manifest.go` — add `"pull_requests": "write"` to `DefaultPerms`.

### `yamledit.AppendScheduleToSkill` contract

```go
// Package yamledit edits cronfoundry.yaml manifests with comment- and
// formatting-preserving precision, using yaml.v3's Node API.
package yamledit

import (
    "errors"

    "github.com/gambtho/cronfoundry/internal/config"
)

var (
    ErrSkillNotFound         = errors.New("skill_path not found in manifest")
    ErrDuplicateScheduleName = errors.New("schedule with this name already exists under skill")
)

// AppendScheduleToSkill appends sched to the schedules: list under the
// SkillEntry whose path matches skillPath in the manifest YAML.
//
// Preserves comments, ordering, indentation, and quoting style of the
// surrounding document — only the inserted lines change in a textual diff.
//
// The marshaled schedule omits zero-valued optional fields so the diff
// stays minimal: empty maps, nil pointers, and zero ints are not emitted.
//
// If the SkillEntry has no schedules: key, one is created.
//
// Returns ErrSkillNotFound if no SkillEntry with the given path exists.
// Returns ErrDuplicateScheduleName if a schedule with sched.Name already
// exists under that skill (caller maps to HTTP 409).
func AppendScheduleToSkill(yamlBytes []byte, skillPath string, sched *config.Schedule) ([]byte, error)
```

The `*config.Schedule` JSON tags (already present) double as YAML tags via `yaml.v3`'s default behavior; if any tags are missing or wrong for YAML use, we add explicit `yaml:"..."` tags as part of this work.

### Run-now backend change

`internal/webapi/schedules.go::runNow`, current lines 282-289:

```go
// before
if resp.StatusCode >= 400 {
    writeErr(w, resp.StatusCode, "trigger failed", "trigger_error")
    return
}
w.WriteHeader(http.StatusAccepted)

// after
if resp.StatusCode >= 400 {
    writeErr(w, resp.StatusCode, "trigger failed", "trigger_error")
    return
}
var trigger struct{ RunID string `json:"run_id"` }
if err := json.NewDecoder(resp.Body).Decode(&trigger); err != nil || trigger.RunID == "" {
    slog.Error("runNow: internal endpoint returned no run_id", "schedule_id", idStr)
    writeErr(w, http.StatusBadGateway, "trigger returned no run_id", "gateway")
    return
}
w.Header().Set("Content-Type", "application/json")
_ = json.NewEncoder(w).Encode(map[string]string{"run_id": trigger.RunID})
```

The internal `/run-now` already returns `{run_id}` (see `internal/api/trigger.go:115-118`); we are catching up the public-facing handler.

### GitHub App manifest update

`internal/githubapp/manifest.go`:

```go
DefaultPerms: map[string]string{
    "contents":      "write",
    "issues":        "write",
    "metadata":      "read",
    "pull_requests": "write", // new
},
```

New installs pick this up automatically. Existing installs (dog5 included) see GitHub's "review permission updates" prompt; the 412 error path on the new endpoint surfaces a CTA linking to the App's permissions-review page.

## Frontend

### Routes

`web/src/main.tsx` adds two routes inside the auth `<Layout>` block:

```tsx
<Route path="/jobs/new" element={<JobNew />} />
<Route path="/jobs/import" element={<JobImport />} />
```

### API client additions

`web/src/lib/api.ts`:

```ts
schedules: {
  // ...existing
  runNow: (id: string) =>
    apiFetch<{ run_id: string }>(`/api/schedules/${id}/run-now`, { method: 'POST' }),
}

skillRepo: {
  proposeJob: (req: ProposeJobRequest) =>
    apiFetch<{ pr_url: string; pr_number: number; branch: string }>(
      '/api/skill-repo/jobs',
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      },
    ),
}
```

`ProposeJobRequest` mirrors the Go `Schedule` struct field-for-field. JSON keys match.

### Pages

- `web/src/pages/JobNew.tsx` — form. Subcomponents:
  - `<DestinationsField>` — repeater. Each row picks a type (`github-issue` etc.) and renders the relevant subform. Validation that at least one destination is configured (matches manifest constraint).
  - `<WritebackField>` — optional toggle, then `path` and `mode` (`append` | `replace`).
  - `<EnvField>` — k/v repeater used for both `env` and `mcp_env`.
  - `<AdvancedFields>` — collapsed by default; surfaces `overlap_policy`, `timeout_sec`, `max_turns`, `copilot_prefix`, `auto_pause`.
- `web/src/pages/JobImport.tsx` — skill dropdown + textarea + "Submit" button. Parses YAML on submit using `js-yaml` (new frontend dep, ~50 KB minified). Parse errors surface inline; textarea retains state.
- `web/src/pages/Jobs.tsx` — wires the buttons:

  ```tsx
  const navigate = useNavigate()
  // ...
  <Button variant="primary" shortcut="N" onClick={() => navigate('/jobs/new')}>+ Add job</Button>
  <Button onClick={() => navigate('/jobs/import')}>+ Import job</Button>
  ```

  Verifies that `<Button shortcut="N">` actually fires the onClick on the keyboard shortcut.

### Run-now wiring

`Jobs.tsx` and `JobDetail.tsx`:

```tsx
const navigate = useNavigate()
const runNow = useMutation({
  mutationFn: api.schedules.runNow,
  onSuccess: (data) => {
    qc.invalidateQueries({ queryKey: ['runs'] })
    navigate(`/runs/${data.run_id}`)
  },
})
```

### New frontend dep

`js-yaml` for `JobImport.tsx`. Verified as the smallest browser-friendly YAML parser; ~50 KB minified.

## Validation strategy

- **Frontend** validation is best-effort UX (required fields, name uniqueness against the loaded `cronfoundry.yaml`). No client-side cron parsing in v1 — invalid expressions are caught by the post-edit `config.ParseManifest` step on the server and surfaced inline. Frontend errors are advisory; the server is the source of truth.
- **Server** validation runs `config.ParseManifest` on the rewritten YAML — the same parser the sync poller uses. No drift between what the form accepts and what the system can actually run. Errors are returned verbatim to the client; the client surfaces them in a top-of-form alert.

We deliberately do **not** extract a `Schedule.Validate()` for per-field client errors in v1. The whole-manifest parse error is sufficient and avoids divergent validation logic.

## Concurrency, conflicts, and known limitations

- **No optimistic locking.** Two operators submitting `+ Add job` at the same instant: GitHub will accept the first PUT-contents but reject the second with 409 (stale file `sha`). We surface 409 to the second client.
- **Open-PR collision not detected.** If the operator opens two PRs adding the same schedule name (e.g. clicks twice across a page reload), both PRs are created. Sync will fail validation on the duplicate after both merge. Known limitation; documented in the response card alongside the PR link.
- **Orphaned branches** if PR creation fails mid-pipeline are not auto-cleaned. Logged at `slog.Warn` for manual cleanup. Out of scope for v1.

## Audit and observability

- One `audit_log` entry per successful PR open: `action="schedule.proposed"`, `target_kind="repo_connection"`, `target_id=<conn.ID>`, `detail={skill_path, schedule_name, pr_url, pr_number, actor=<login>}`.
- One `slog.Info` entry per successful PR open mirrored to the serve container logs.
- `slog.Warn` for orphaned-branch failures and failed audit writes.
- 412 responses include `review_url`; we don't separately log them since they're operator-facing.

## Tests

### Backend

- `internal/yamledit/append_schedule_test.go`:
  - Append to an existing skill that already has schedules — diff shows only the inserted lines.
  - Append to an existing skill whose `schedules:` key is absent — creates the key with the new entry as the only element.
  - `skillPath` not found in manifest — returns `ErrSkillNotFound`.
  - Duplicate `schedule.Name` under the target skill — returns `ErrDuplicateScheduleName`.
  - Comments above and below the target skill survive untouched.
  - File without trailing newline gets one in the output.
  - Manifest is `kubernetes`-style (anchors, references) — anchors are preserved.
  - Round-tripped YAML re-parses cleanly via `config.ParseManifest`.

- `internal/skillrepo/client_test.go`:
  - `GetFile` — returns content + sha + head sha.
  - `CreateBranch` — handles 422 already-exists (treat as success or 409? — per "no collision check" answer above, we 409 the user).
  - `PutFile` — sends correct body; surfaces 409 (stale sha) cleanly.
  - `CreatePR` — happy path; 422 if PR already open; 403 → `ErrPermissionRequired`.
  - All four use `httptest.Server` stubs in the same style as `internal/publish/githubissue.go`.

- `internal/webapi/skill_repo_jobs_test.go`:
  - 400 on empty `skill_path`, empty `schedule.name`, malformed JSON.
  - 400 when post-edit `ParseManifest` fails (e.g. bad cron in submitted schedule).
  - 409 when `yamledit` returns `ErrSkillNotFound`.
  - 412 when `skillrepo.Client` returns `ErrPermissionRequired`.
  - 502 on a generic GitHub 500.
  - Happy path: validates body, calls each pipeline step in order, writes audit log, returns `{pr_url, pr_number, branch}`.
  - Audit log is written even if PR creation succeeds; not written on failure paths.

### Frontend

- `Jobs.test.tsx`:
  - `+ Add job` navigates to `/jobs/new`.
  - `+ Import job` navigates to `/jobs/import`.
  - Pressing `N` triggers `+ Add job`'s onClick.
  - Run-now success navigates to `/runs/<id>` from the returned mock.
- `JobNew.test.tsx`:
  - Required fields block submission.
  - Form serializes to the expected `ProposeJobRequest` shape.
  - Advanced fields are hidden by default and submit with their zero values omitted.
  - 412 response renders the permission-review CTA.
  - 400 response renders the inline parser error.
- `JobImport.test.tsx`:
  - Invalid YAML surfaces inline; submit button stays disabled.
  - Valid YAML serializes to the same `ProposeJobRequest` shape as `JobNew`.
- `JobDetail.test.tsx`:
  - Run-now success navigates to `/runs/<id>`.

## Sequencing and PR shape

This is one PR. It touches:

- 5 new backend files: `internal/yamledit/append_schedule.go`, `internal/yamledit/append_schedule_test.go`, `internal/skillrepo/client.go`, `internal/skillrepo/client_test.go`, `internal/webapi/skill_repo_jobs.go` (handler) and `internal/webapi/skill_repo_jobs_test.go`.
- 3 modified backend files: `internal/webapi/server.go` (route registration), `internal/webapi/schedules.go` (Run-now forwards run_id), `internal/githubapp/manifest.go` (adds `pull_requests:write`).
- 6 new frontend files: `web/src/pages/JobNew.tsx`, `web/src/pages/JobImport.tsx`, `web/src/pages/JobNew.test.tsx`, `web/src/pages/JobImport.test.tsx`, plus `web/src/components/forms/DestinationsField.tsx` and `web/src/components/forms/EnvField.tsx` (shared subcomponents). `<WritebackField>` and `<AdvancedFields>` live inline inside `JobNew.tsx` since they're only used there.
- 5 modified frontend files: `web/src/main.tsx` (routes), `web/src/pages/Jobs.tsx` (button onClicks + Run-now nav), `web/src/pages/Jobs.test.tsx` (covers nav + shortcut), `web/src/pages/JobDetail.tsx` (Run-now nav), `web/src/lib/api.ts` (new `skillRepo.proposeJob` + updated `runNow` return type).
- `web/package.json` and `web/package-lock.json` for the new `js-yaml` dep.

Estimated diff size: ~1500–2000 LOC including tests. Worth keeping in one PR because the wiring (handler + form + import + run-now) all unblock each other and shipping pieces independently leaves the dashboard in a half-working state.

## Out of scope, explicitly

- Editing or deleting an existing schedule via UI. (Handled today by editing `cronfoundry.yaml` in the skill repo.)
- Creating a brand-new skill (`SKILL.md`) via UI.
- Inline cron-expression preview with "next 3 fires" (nice-to-have; deferred).
- Optimistic locking with the `cronfoundry.yaml` file SHA returned in a preview step.
- Open-PR collision detection.
- Automatically merging the PR after creation.
- Triggering a sync on the connection right after the PR merges (the existing webhook flow handles this).

## Sharp edges and things to double-check during implementation

- `yaml.v3` and `sigs.k8s.io/yaml` will both be in the build. Verify the `*config.Schedule` JSON tags produce sensible YAML through `yaml.v3` directly (they should, since `yaml.v3` reads JSON tags as a fallback when no `yaml` tag is present, but worth a focused test).
- The `<Button shortcut="N">` keyboard handler should be exercised by a test — if it's not actually wired to fire the onClick, fix it as part of this work.
- `pull_requests:write` may not be sufficient on its own for `POST /pulls` if the App is installed without the repo selected. The 412 code-path should also fire when the App reports `installation does not have access`, with a CTA to the install-config page (different URL than permissions-review). Implementer should test both paths against a real install.
- The starter `cronfoundry.yaml` in `gambtho/skills` has a header comment `# Starter smoke skill — adjust cron, destinations, and writeback before going live.` — the yamledit fixture suite must include this exact header so we catch any comment-loss regression.
