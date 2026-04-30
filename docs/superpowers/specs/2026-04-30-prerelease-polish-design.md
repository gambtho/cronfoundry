# Pre-release Polish Pass — Design

**Status:** Draft
**Date:** 2026-04-30
**Owner:** TBD
**Target:** Internal team production rollout (medium scale, plan-for-medium)

## Goal

A new operator runs `bash <(curl -fsSL …/install.sh)`, answers ~3 unavoidable
prompts (Azure subscription, region, the OAuth device-flow user code), and
ends with a green Copilot Enterprise run firing every 5 minutes. Wall time:
< 15 minutes. No copy-paste of IDs, no leaving the terminal except for one
GitHub App manifest authorization and one Copilot device-flow approval.

The canonical install path being verified is **Azure deploy + GitHub App +
Copilot Enterprise as the LLM provider**. `install.sh` is the product
surface; the doc explains what the script does, not what the operator does.

## Definition of done

I redeploy from a clean Azure subscription using only `install.sh`, three
times in one session, without consulting a doc during the run. Each round
ends with a green run within 15 minutes. The third round is silent except
for the unavoidable browser interactions.

## Scope

**In scope:**

1. Friction elimination in `install.sh` (Phase 1).
2. Live Azure dogfood loop until clean (Phase 2).
3. UI polish + onboarding/empty-state UX, partial shadcn migration (Phase 3).
4. Docs gap-fill + accuracy pass (Phase 4).
5. Three small operator features: auto-pause, run replay, token/cost (Phase 5).

**Out of scope (remains Deferred):**

HA / replicas / DR drills, multi-cloud, Helm/AKS, SSO beyond GitHub, full
security audit, hosted SaaS, image signing/SBOM, conditional routing,
UI-managed schedules, rich Block Kit / Adaptive Cards.

## Phase order and dependencies

Phase 1 → Phase 2 (these gate everything else; no point dogfooding a script
that still has known friction). Phases 3, 4, 5 run in parallel after Phase 2
starts. Phase 4 (docs) lands last because it depends on the polished UI for
screenshots and on the dogfood punch list for the troubleshooting guide.

```
Phase 1 ──► Phase 2 ──┬─► Phase 3 (UI)
                      ├─► Phase 5 (operator features)
                      └─► Phase 4 (docs) ◄── (depends on 3 + 5 for screenshots)
```

---

## Phase 1 — Friction elimination in `install.sh`

The script becomes a state machine over `~/.cronfoundry-quickstart-state`.
Each step is idempotent and resumable; re-running picks up at the first
incomplete step.

**Major reorder vs current `scripts/quickstart-copilot.sh`:** Bicep deploys
*before* GitHub App registration. The App is then registered via the
manifest flow with the real FQDN as its callback/webhook URL — no
post-deploy URL patch step needed.

### New step order

| # | Step | Today | After |
|---|------|-------|-------|
| 1 | Prereqs | OK | Print detected versions of every tool. |
| 2 | Azure login | OK | Keep. |
| 3 | Subscription select | `read -p` | Keep. |
| 4 | Env suffix | `read -p` (default `copilot1`) | Keep. |
| 5 | Region | `read -p` (default `swedencentral`) | Keep. |
| 6 | Master key | generated | Keep. |
| 7 | Postgres password | generated | Keep. |
| 8 | Image tag | resolved from latest release | Keep. |
| 9 | **Bicep deploy** | §14 in current script | Moved up. Streams progress; FQDN captured to state file. |
| 10 | **`admin init`** + revision restart | §15 today | Auto-tightens the Postgres firewall rule to operator IP after migrations succeed. |
| 11 | **GitHub App manifest flow** | §5 (manual, 9 sub-steps, 4 paste-back values) | Script opens browser to a one-shot manifest URL with permissions/events pre-declared, listens on `0.0.0.0:8765` for the manifest-conversion redirect (so WSL2 browsers can reach it; localhost on macOS/Linux works too), receives App ID + private key + webhook secret + client secret in the redirect payload. **Real FQDN baked in as callback/webhook URLs at registration time.** Zero copy-paste. |
| 12 | **Skill repo prompt** | `read -p` | Same prompt; validates App is installed via `gh api`. |
| 13 | **Auto-discover installation ID** | `read -p` | `GET /app/installations` (App JWT); pick install on the operator's repo. If multiple, prompt. |
| 14 | **Reports repo prompt** | `read -p` | Same prompt; validates App install. |
| 15 | **Connect repo via admin CLI** | UI click in §17 | `cronfoundry admin connect-repo` against deployed instance. |
| 16 | **Webhook secret via admin CLI** | UI click in §17 | `cronfoundry admin set-secret github_webhook_secret` (value from manifest flow). |
| 17 | **Copilot Enterprise device-flow** | UI click in §17 | New `cronfoundry admin connect-copilot --prefix copilot` CLI command. Operator sees device code in terminal, opens URL, types code. |
| 18 | **Auto-push starter skill** | manual yaml authoring in §17 | `gh api` push of `cronfoundry.yaml` + `skills/smoke/SKILL.md` to a `cronfoundry-quickstart` branch on the skill repo, then merge or PR. Skip cleanly if `cronfoundry.yaml` already exists. |
| 19 | **Wait for first green run** | manual UI watch | Poll `GET /api/runs?limit=1` until status flips to `succeeded`. Print `✅ first run green in MM:SS — open https://<fqdn>/`. |

### New CLI surface

- `cronfoundry admin connect-copilot --prefix <name>` — runs Copilot
  Enterprise device-flow against the deployed instance, stores token via
  the existing internal API or directly through the provider package +
  admin DB connection.
- The script uses existing admin commands where possible
  (`connect-repo`, `set-secret`) rather than going through the UI.

### Teardown

`cronfoundry-quickstart down --env copilot1` runs:

1. `az group delete --name rg-cronfoundry-<env> --yes --no-wait`.
2. Revoke the GitHub App installation via App JWT
   (`DELETE /app/installations/{id}`).
3. Delete the state file `~/.cronfoundry-quickstart-state-<env>`.

State files are now per-env so concurrent envs are clean.

### Idempotency contract

- State file holds: subscription ID, env suffix, region, image tag,
  Postgres password (encrypted with master key), master key reference,
  FQDN, App ID, install ID, repos, completed-step bitmap.
- Re-running the script reads the bitmap and skips completed steps.
- Each step has a verifier (`is this already done?`) so a partial state
  from a crashed run is detectable. Steps with side effects on Azure use
  Azure resource lookups as their verifier (idempotent by name).

### Diagnostics

Every step that can fail prints, on failure: `step: <name>`,
`expected: <X>`, `got: <Y>`, `next: <copy-pasteable fix>`. The script
exits with a non-zero code and an obvious "resume with: `bash install.sh`"
hint.

### Risks

1. **Manifest flow callback on WSL2.** The manifest redirect is
   `localhost:8765`. WSL2 forwards localhost ports into Windows; the
   script must bind `0.0.0.0:8765` so the Windows browser can reach it.
   Test on macOS, Linux, WSL2 in Phase 2.
2. **`PATCH /apps/{slug}` for App URL updates.** No longer needed —
   manifest registers with real URLs. If a future flow needs it, fall back
   to printing manual instructions.
3. **Skill repo with branch protection or existing manifest.** Detect
   and skip the auto-push; print "already configured — see existing
   manifest at <link>".
4. **Copilot Enterprise device-flow token storage.** Verify the new
   `connect-copilot` admin command can persist the token in the same
   shape the runner expects (provider prefix + access token + refresh
   path). Existing UI device-flow code in `internal/llm/copilotenterprise`
   is the reference.

### Tests

- Unit: state-file load/save round-trip, step-bitmap operations,
  manifest-redirect parser.
- Integration: e2e test that boots a docker-compose stack and runs
  `cronfoundry admin connect-repo` + `set-secret` + `connect-copilot`
  against it, asserts the resulting DB rows are correct.
- Manual: Phase 2 dogfood. No automated way to test the live Azure +
  GitHub App + Copilot side without burning real resources.

---

## Phase 2 — Live Azure dogfood loop

Procedure, not deliverable list.

1. **Round 1 — instrumented run.** Fresh Azure subscription or fresh RG
   with a new env suffix. Run `install.sh` end-to-end. Record every place
   where the script paused unexpectedly, an error didn't tell the
   operator what to do, output scrolled too fast, a retry was needed, the
   wall-clock estimate was wrong, or I had to consult a doc during the
   run. Output: numbered punch list committed as
   `docs/superpowers/specs/<date>-quickstart-dogfood-round1.md`.
2. **Fix the punch list.** Each item → small commit on a feature branch
   with a focused PR. Most fixes in `scripts/quickstart-copilot.sh`,
   some in `cmd/cronfoundry/admin/*`, a few in Bicep.
3. **Round 2 — verification run.** `az group delete`, new env suffix,
   re-run. Goal: zero unplanned interventions. Time it.
4. **If round 2 isn't clean → round 3.** Stop after round 3 even if not
   perfect — diminishing returns. File remaining issues as Deferred.

**Test matrix:** macOS + Linux mandatory. WSL2 best-effort via a
`--platform-check` flag that prints the env detected and what it'll do
differently.

**Cost guardrail:** ~$5 per round on minimal SKUs (current Bicep
defaults). Tear down between rounds.

**Exit criteria:** three consecutive green deploys from clean in one
session, no doc reference, < 15 minutes wall time.

---

## Phase 3 — UI polish + onboarding

### Track A — Partial shadcn migration

Adopt shadcn for the high-value components, leave the rest. Add deps:
`@radix-ui/react-*` (transitively via shadcn), `class-variance-authority`,
`clsx`, `tailwind-merge`, `lucide-react`. Configure shadcn's CSS-variable
theme tokens in `tailwind.config.ts`.

**Components migrated:**

- `Dialog` (replaces existing modals via `SecretModal`).
- `AlertDialog` (replaces `ConfirmDialog`).
- `Sheet` (replaces ad-hoc run detail drawer; `LogTail` lives inside).
- `DropdownMenu` (user menu, row actions on Schedules/Runs/Repos).
- `Toast` (replaces alert paths; success/error notifications).
- `DataTable` (Runs page, Audit page; sort + filter built in).
- `Form` + `react-hook-form` + `zod` (Repos connect, Secrets add,
  Schedule override).

**Components left as-is:**

- `Layout`, `Login`, navigation chrome — minor token/spacing pass only.
- `RunStatusBadge` — keeps current API; restyled with shadcn `Badge` base.
- `LogTail` internals (SSE handling) — unchanged; lives in `Sheet`.

**Theme:** shadcn default neutral + one accent (TBD; orange/amber
matches the "foundry" name). Single light theme initially; dark mode
can flip via CSS-variable swap if a later phase wants it.

### Track B — Onboarding empty-state UX

**Dashboard, fresh install (zero schedules):** replace blank with a
3-step inline onboarding card:

1. ✅ **Provider connected** — green check or "Connect provider" link.
2. ✅ **Skill repo connected** — green check or "Connect repo" link.
3. ⏳ **Waiting for first run** — "schedule fires in 3:24" countdown
   with a link to the Runs page.

After the first successful run, the card collapses to a small "First run
completed at HH:MM" toast and the dashboard shows the normal state.

**Run-failure surfacing:** Runs list — hovering the status badge shows
the first line of the failure reason; clicking opens the detail Sheet.
The failure timeline highlights `manifest.set`, `secret.fetched`,
`secret.denied` events prominently with links to fixes (e.g. "secret
`slack_webhook` not found" → link to Secrets page pre-filtered).

**"Why is my run not firing?"** On the Schedules view (or a panel on
Runs), show the next scheduled fire time per schedule plus a "Run now"
button (Phase 5b — copy will reference it).

### Out of scope for Phase 3

- No new pages, no router changes.
- No theme switcher.
- No animation library beyond CSS transitions.
- Mobile responsiveness: light pass only; operator UI is desktop-first.

### Tests

- vitest component tests for new shadcn-based components (existing
  `LogTail.test.tsx` is the pattern).
- Storybook is **not** added (would expand scope).
- Visual regression: skipped; tracked manually via screenshots in PRs.

---

## Phase 4 — Docs gap-fill + accuracy

### Audit (find drift, fix inline)

- README architecture list — add `audit`, `bootstrap`, `githubapp`,
  `jobdispatch`, `mcp`, `metrics` to the tree; remove anything renamed
  or deleted.
- README "Quick start (local dev)" — verify against current `make dev`
  and `cronfoundry admin init` output.
- README "Quick start (standalone runner)" — verify against current
  `cronfoundry-runner run` flags.
- All design specs in `docs/superpowers/specs/` — status header present
  and accurate (PR #37 added them; verify none are stale).
- Cross-link check: every "see X for details" pointer resolves.
- Env var reference: every `CRONFOUNDRY_*` env var the binary reads is
  documented; no documented var is unused. Generate
  `docs/reference/env-vars.md` from code, link from README.
- Manifest reference: every key under `cronfoundry.yaml` documented.
  Generate skeleton from `internal/config/*.go`.

### New docs

1. `docs/guides/troubleshooting.md` — symptom → likely cause → fix
   table. Seeded from the Round-1 dogfood punch list and existing tables
   in `quickstart-copilot.md`.
2. `docs/guides/schedule-authoring.md` — full reference for
   `cronfoundry.yaml` and `SKILL.md`: every field, default, semantics,
   examples. Includes destination-template variable table, secret
   resolution, overlap policy, writeback semantics.
3. `docs/guides/operator-runbook.md` — first-24-hours runbook: read the
   audit log, identify a failing schedule, rotate the master key, add a
   new operator, upgrade the deployed image, back up and restore
   Postgres.
4. `docs/reference/env-vars.md` and `docs/reference/manifest.md` —
   generated reference, linked from README.

### Screenshots

3 added to README + both quickstarts after Phase 3 lands:

- Dashboard with a recent run.
- Runs page with a green + a `partial_failure`.
- Run detail Sheet with live log tail.

### Out of scope for Phase 4

- No mkdocs/Docusaurus/docs-site reorganization. Markdown in `docs/`
  served by the existing GitHub Pages setup (PR #33).

---

## Phase 5 — Three small operator features

### 5a — Auto-pause on consecutive failures

**Manifest field:** `auto_pause_after: 3` per schedule (default disabled
when omitted or `0`).

**Schema:** add columns to schedules table — `consecutive_failures int
NOT NULL DEFAULT 0`, `paused_at timestamptz NULL`, `paused_reason text
NULL`.

**Scheduler tick:** skip schedules with `paused_at IS NOT NULL`. On
every run finish, increment or reset counter; if `>= auto_pause_after`,
set `paused_at = now()`, `paused_reason = 'auto-paused after N
consecutive failures'`. Emit an `audit` event.

**API:** `POST /api/schedules/{id}/pause` and
`POST /api/schedules/{id}/resume`.

**UI:** schedule row shows a "paused" pill with a "Resume" button.
Resume clears the pause fields and resets the counter.

**Tests:** scheduler unit test (counter increments/resets correctly,
threshold honored), e2e test (force 3 failures, observe pause).

### 5b — Manual run replay

**API:** `POST /api/schedules/{id}/run-now` returns `{run_id}`. Reuses
the existing scheduler run-dispatch path.

**UI:** "Run now" button on every schedule row; on the Runs page filter
for that schedule. Clicking redirects to the run detail Sheet with
live-tail.

**Audit:** emit `schedule.run_now` with the operator who triggered it.

**Edge cases:** an already-running run with `overlap_policy: skip` —
return 409 with a "skipped: already running" body; UI shows a
non-blocking toast.

**Tests:** webapi test that a viewer cannot trigger; admin can; emitted
audit event is correct.

### 5c — Token/cost surfacing per run

**Schema:** add columns to runs table — `prompt_tokens int NULL`,
`completion_tokens int NULL`, `total_tokens int NULL`,
`usd_cost numeric(10,6) NULL`.

**Provider parsers:** OpenAI, Anthropic, Foundry, Copilot Enterprise —
extract token counts from response. Verify each provider populates
these (Copilot Enterprise may be best-effort).

**Pricing:** static `internal/llm/pricing/model_pricing.json` ships
with the binary, mapping `provider/model → ($/1k_prompt,
$/1k_completion)`. Display as "estimated."

**UI:** run detail shows `tokens: 2,341 in / 412 out — est. $0.0034`.
Runs list adds a "$" column (toggleable). Dashboard adds a "tokens this
week / est. cost this week" tile.

**Tests:** provider parsers extract tokens correctly; pricing lookup
falls back to `null` (display "—") for unknown models; aggregation
queries.

---

## Risks (cross-phase)

1. **GitHub App manifest flow blocked by org policy.** Some orgs
   restrict App registration to org admins. Mitigation: detect via
   `gh api user/orgs` permissions and print a clear "ask your org admin
   to register the App, then run with `--app-id …`" fallback.
2. **Copilot Enterprise availability.** Operator must have an active
   seat. Detect early via a probe call before §11.
3. **shadcn migration breakage.** Existing pages must not regress.
   Mitigation: migrate one page at a time, each PR is reviewable in
   isolation, run vitest after each.
4. **Pricing JSON staleness.** Cost displayed as "estimated" with a
   tooltip noting the JSON's last-updated date. Document update process
   in `operator-runbook.md`.

## Success criteria (recap)

- Three consecutive clean Azure deploys via `install.sh` in one session,
  < 15 minutes wall time, no doc reference during deploy.
- Dashboard on a fresh install actively guides an operator to their
  first green run.
- A schedule that flaps 3× auto-pauses; the operator gets a one-click
  Resume.
- A run page shows token usage and an estimated cost.
- Troubleshooting, schedule-authoring, and operator-runbook docs exist
  and are accurate.

---

## Out of scope (explicit non-goals)

- HA / multi-replica serve / DR drills.
- Multi-cloud (AWS, GCP).
- Helm / AKS-native deploy path.
- SSO beyond GitHub.
- Conditional destination routing (`on_failure:`, `on_success:`).
- UI-managed schedules (per-skill `managed: ui`).
- Rich Block Kit / Adaptive Cards destination formatting.
- Hosted multi-tenant SaaS.
- Image signing / SBOM.
- Mobile/tablet responsive UI.

These remain in the design-spec Deferred list and are not blocked by
this work.
