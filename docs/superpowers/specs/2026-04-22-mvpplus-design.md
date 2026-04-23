# CronFoundry mvpplus — Design

**Status:** Proposed  
**Date:** 2026-04-22  
**Author:** gambtho (brainstormed with Claude)

## Overview

mvpplus is the first post-MVP feature wave, targeting solo engineers and small
teams who are already self-hosting CronFoundry. The goal is to close the three
most visible gaps that limit adoption: schedule management without YAML edits,
smarter notification routing, and output that looks good in Slack/Teams.

Features are grouped into three phases. This doc covers **mvpplus-1** in full
and names the subsequent phases for sequencing context.

---

## Feature Phases

| Phase | Features | Goal |
|-------|----------|------|
| **mvpplus-1** | UI schedule edits (F6), conditional routing (F5), rich formatting (F4) | Core UX polish for solo engineers |
| **mvpplus-2** | Additional destinations: email, PagerDuty, custom HTTP (F8) | Widen "fits my stack" reach |
| **mvpplus-3** | Pluggable secret backends + LLM prompt redaction (F9), image signing / SBOM (F13) | Production hardening |
| **Fully deferred** | SSO/Entra (F7), Helm/AKS (F10), multi-cloud (F11), hosted SaaS (F12) | Different buyer / different business case |

---

## mvpplus-1

### Feature 6: UI Schedule Edits

Users can edit a small set of schedule fields directly in the UI without
touching `cronfoundry.yaml`. YAML remains the source of truth for structure
(destinations, provider, model, writeback). The UI owns a delta on top.

**Editable fields:** cron expression, timezone, enabled/paused, timeout_sec.

**Data model:** `schedule` table gains a `ui_overrides_json` column
(`{cron?, timezone?, enabled?, timeout_sec?}`). The scheduler and runner merge
YAML-derived values with UI overrides at read time; UI wins on conflicts.

**YAML push behavior:** A push that resyncs a schedule leaves `ui_overrides_json`
untouched — UI edits survive YAML pushes. A push that removes a schedule from
YAML deletes the row entirely, including overrides.

**API:** `PATCH /api/schedules/{id}/overrides` — accepts any subset of editable
fields, validates, persists, audit-logs. `DELETE /api/schedules/{id}/overrides`
resets to YAML-only.

**UI surface:** Schedule detail page gains an "Edit" button opening a small
form: cron field with human-readable preview, timezone dropdown, timeout input,
enable/disable toggle. Schedule list shows a badge when UI overrides are active
so users know the effective config differs from YAML.

**Path to C (new-schedule wizard):** Once this is working, the wizard is
additive — it creates a YAML-backed row rather than applying a delta. No
rework required.

---

### Feature 5: Conditional Destination Routing

Each destination in `cronfoundry.yaml` can declare a `when:` condition. The
runner evaluates it after the LLM call and skips destinations whose condition
doesn't match.

**Conditions:** `on_success`, `on_failure`, `always` (default).

**Run outcome for routing:** determined after the LLM call — `succeeded` if
the LLM responded without error; `failed` if the LLM errored or timed out.
Destination failures don't affect routing for subsequent destinations.

**Config shape:**
```yaml
destinations:
  - slack:
      secret: slack_digest_webhook
      when: on_success
  - slack:
      secret: slack_alerts_webhook
      text: "Run failed: {{ skill.name }}"
      when: on_failure
  - github-issue:
      repo: myorg/reports
      when: always
```

**Data model:** `when` stored as a field inside `destinations_json` on the
`schedule` row. No schema change.

**UI:** No UI surface for mvpplus-1 — `when:` is YAML-only. The run detail
page shows "skipped (condition: on_failure not met)" for destinations that were
routed past.

---

### Feature 4: Rich Destination Formatting + Multi-Output Skills

**Slack Block Kit:** Slack destinations render output as Block Kit by default (`format: blocks` is the default for Slack; `format: text` opts back to plain text).
Long outputs auto-split into multiple `section` blocks (3000 chars/block max).
A header block carries the skill name and run date.

**Teams Adaptive Card upgrade:** Teams destinations default to a minimal card (current behavior). With `format: card`, the runner builds a richer Adaptive Card: title `TextBlock`, a `FactSet` for run metadata (skill
name, date, run ID), and the output in a `Container`. No user-authored card
JSON required.

**Multi-output skills:** A skill can emit multiple named sections using
`<output name="section-name">...</output>` XML blocks. Each named section can
be routed to a specific destination via an `output:` field in the destination
config. If a skill emits no `<output>` blocks, the entire LLM response is
treated as a single unnamed output and all destinations receive it.

**Config shape:**
```yaml
destinations:
  - slack:
      secret: slack_webhook
      format: blocks
      output: summary         # routes <output name="summary"> to Slack
  - github-issue:
      repo: myorg/reports
      output: full_report     # routes <output name="full_report"> to the issue
```

**Parser:** The runner output parser extracts named `<output>` blocks alongside
the existing `<memory>` block. Named blocks and the memory block are all
stripped before computing "remaining text" (which becomes the unnamed output
for destinations with no `output:` field).

**Data model:** `format` and `output` fields added to `destinations_json`. No
schema change to `schedule` table structure.

---

## Success Criteria

**UI schedule edits:**
- A user can change a schedule's cron expression in the UI and see the next
  fire time update immediately, without editing `cronfoundry.yaml`.
- A YAML push that modifies other fields on the same schedule does not
  overwrite the UI-set cron.
- The schedule list clearly indicates which schedules have UI overrides active.

**Conditional routing:**
- A schedule configured with `when: on_failure` on one destination and
  `when: on_success` on another routes correctly — only the matching
  destination fires.
- The run detail page shows skipped destinations with the reason.

**Rich formatting:**
- A Slack notification from a skill using `<output name="summary">` renders
  as a Block Kit message with a header and the summary section only.
- A GitHub issue receives the `full_report` output section, not the summary.
- A skill with no `<output>` blocks continues to work — all destinations
  receive the full response.

---

## Out of Scope for mvpplus-1

- New-schedule wizard (UI creates schedules from scratch) — deferred to a
  later iteration once the override model is proven.
- SSO / Entra ID — fully deferred.
- Email, PagerDuty, custom HTTP destinations — mvpplus-2.
- KV-proxy sidecar, image signing — mvpplus-3.
