# CronFoundry — Product Requirements

**Status:** Proposed
**Date:** 2026-04-19
**Author:** gambtho
**Companion document:** [`2026-04-19-cronfoundry-design.md`](./2026-04-19-cronfoundry-design.md) (technical design)

## One-Line Pitch

A self-hostable Azure service that runs LLM "skills" on a cron schedule, driven
by a GitHub repo, with outputs posted to the places your team already watches
(GitHub issues, Slack, Discord, Teams) and learnings committed back to the
repo.

## Problem

Engineers increasingly have *scheduled* work for LLMs — weekly digests, daily
monitoring summaries, overnight triage, backlog grooming, on-call handoffs,
periodic audits. Existing tools don't cover this use case cleanly:

- **Claude routines / ChatGPT scheduled tasks**: locked to one provider, no
  GitOps config, limited output destinations, no durable learning memory, no
  self-hosting.
- **Agent CLIs (opencode, Claude Code, aider)**: designed for interactive
  sessions; no scheduler, no multi-destination publisher, no team-shareable
  config model.
- **Generic cron + shell scripts**: works, but every team reinvents prompt
  management, provider auth, secret handling, output formatting, and
  observability.
- **n8n / Zapier / Power Automate with LLM steps**: too general-purpose; the
  LLM-specific concerns (prompt files, streaming, token accounting, retries on
  rate-limit) are not first-class. Config lives inside the tool, not in git.

The gap is a **narrow, opinionated tool** that treats "scheduled LLM skill" as
the primary object, with GitOps config, BYOK provider flexibility, and a clean
publisher story for team-visible output.

## Target Users

### Primary: "Platform / DevEx Engineer" (MVP focus)

- Works on a small engineering team, responsible for team-wide tooling.
- Comfortable with GitHub, PRs, YAML, cron syntax, Azure or another cloud.
- Has an OpenAI or Anthropic API key, possibly Azure AI Foundry via their org.
- Wants to deploy once, let teammates add scheduled skills via PR, and not
  think about it again.
- Blocks on: "how do I set up a weekly digest that posts to our Slack without
  standing up a whole custom service?"

### Secondary: "LLM-curious team lead" (post-MVP)

- Less hands-on with infra; wants to edit a schedule or pause a skill via UI.
- Needs a readable dashboard of what's running and what's failing.
- Will be served better by the post-MVP "UI-managed schedules" feature
  (deferred from MVP).

### Non-users

- End-users of LLMs with no GitHub account / no cloud infra.
- Teams needing multi-step agentic workflows with human-in-the-loop steps —
  CronFoundry is a single-run fan-out-publish system, not a workflow engine.
- Anyone needing sub-minute scheduling latency or event-driven (non-cron)
  triggers — future consideration, not MVP.

## Goals

**Product goals for the MVP** (ordered):

1. **A Platform Engineer can stand up their first working scheduled skill in
   one afternoon**, end to end, from empty Azure subscription to a GitHub
   issue + Slack message landing in a channel.
2. **Config lives in git, reviewable like code.** Schedule changes go through
   the same PR process as any other infrastructure change.
3. **Secrets never leave the secure path.** LLM API keys and webhook URLs land
   in Azure Key Vault, never transit the DB, never display in the UI after
   creation.
4. **Skill runs are observable and replayable.** Every fire leaves a durable
   record (status, duration, cost, destinations posted, write-back commit)
   that teammates can inspect without logging into Azure.
5. **Cost is boring.** Light use (a handful of skills, a few fires per day)
   costs under $100/month on Azure.

**Success metrics** (measurable post-launch):

- Median "empty-subscription to first green run" time < 2 hours for a new
  self-hoster (measured via structured walkthrough feedback).
- Run success rate (excluding user-config errors) > 99% over a rolling 7 days.
- Run-to-destination publish latency P95 < 5 seconds.
- Per-run cost overhead (everything except the LLM call) < $0.01.
- Zero secret-value leaks in logs, audits, or UI responses (verified via
  targeted red-team review before first release).

## Non-Goals

- General-purpose cron or arbitrary shell execution.
- LLM gateway concerns (caching, routing, fallback across providers).
- Workflow-engine features (DAGs, fan-in, conditional steps, human approval).
- Prompt authoring / editing in the UI.
- Interactive / REPL use of skills.
- Sub-minute scheduling precision or event-driven triggers.

## Key Use Cases

### UC-1: Weekly engineering digest

A Platform Engineer creates a `weekly-digest` skill that pulls recent activity
(via a tool in the skill's prompt or pre-computed input files in the repo),
summarizes it, posts to a GitHub issue with a `digest` label, cross-posts a
link to `#eng-leadership` on Slack, and appends the summary to a `memory.md`
in the repo so next week's run has context.

### UC-2: On-call handoff brief

A daily skill reads yesterday's alerts (from a context file the alerting
pipeline drops into the repo), produces a structured handoff brief, and posts
it as an Adaptive Card to the on-call Teams channel. Runs on weekdays at
08:30 PT, skips if the previous run is still in-flight.

### UC-3: Backlog grooming suggestions

A weekly skill reviews the current GitHub project board (via tools that will
come in the MCP fast-follow release), identifies stale items, and files an
issue with suggested actions. Until MCP ships, the user runs this in "digest
mode" from pre-fetched data and graduates to tool-use when available.

### UC-4: Ad-hoc / testing

A user writes a new skill, pushes it in a draft PR, and uses the UI's
"Run now" button to iterate without committing schedules. Each run is
isolated and leaves the same audit trail as a scheduled fire.

### UC-5: Team-visible failure awareness

A skill that posts to Slack also has a `critical: true` flag (post-MVP) that
escalates repeated failures to a secondary destination. In MVP, the dashboard
surface is enough: a teammate glances at the UI, sees three failed runs, and
pauses the schedule until the prompt is fixed.

## Functional Requirements

### FR-1: Repo connection & discovery

- FR-1.1 An admin must be able to install the CronFoundry GitHub App on a repo
  and see it appear in the UI as a "connected repo" within 60 seconds.
- FR-1.2 CronFoundry must discover all skills declared in `cronfoundry.yaml`
  at the repo root after any push.
- FR-1.3 The repo manifest must support **many skills per repo**, each with
  zero or more schedules.
- FR-1.4 Schedule config is read-only in the UI; authoritative source is the
  YAML file at the connected repo's default-branch HEAD.
- FR-1.5 Skills with `schedules: []` must be runnable via UI manual trigger
  but never fire automatically.

### FR-2: Scheduling

- FR-2.1 Standard cron syntax, per-schedule timezone, with DST-safe next-fire
  computation.
- FR-2.2 Per-schedule overlap policy: `skip` (default), `queue`, `concurrent`.
- FR-2.3 Per-schedule wall-clock timeout, default 10 min, max 1 hour.
- FR-2.4 Scheduler ticks are idempotent; duplicate or stuttering ticks must
  not produce duplicate runs.
- FR-2.5 Admins must be able to pause / resume a schedule from the UI without
  touching the repo; pause is a runtime flag, not a config change.
- FR-2.6 Admins must be able to manually trigger a run from the UI;
  manual-triggered runs are audit-logged with the admin's identity.

### FR-3: LLM provider support (MVP)

- FR-3.1 Support OpenAI, Anthropic, and Azure AI Foundry as providers, all
  via user-supplied keys (BYOK).
- FR-3.2 Per-schedule selection of provider + model.
- FR-3.3 Streaming completions, with per-run token counts and estimated cost
  recorded.
- FR-3.4 Provider failures (429 / 5xx) must retry with exponential backoff
  within the run's timeout budget; no cross-provider fallback.
- FR-3.5 The provider-adapter interface must be stable enough that adding
  GitHub Copilot Enterprise (post-MVP) requires no changes to scheduler or
  runner plumbing.

### FR-4: Output destinations

CronFoundry must support the following destination types, each configurable
per-schedule with its own secret reference:

- FR-4.1 **GitHub issue** — opens an issue in any repo the CronFoundry GitHub
  App is installed on, with templated title, body, labels, optional assignees.
- FR-4.2 **Slack** — posts to an Incoming Webhook URL with markdown text.
- FR-4.3 **Discord** — posts to a webhook with markdown content, optional
  username override, respecting the 2000-char limit.
- FR-4.4 **Teams** — posts an Adaptive Card payload to a Power Automate flow
  webhook ("When a Teams webhook request is received" + "Post adaptive card").
- FR-4.5 A single schedule must support multiple destinations simultaneously.
- FR-4.6 A destination failure must not block other destinations for the same
  run; a partial success is recorded as `partial_failure` status.
- FR-4.7 Templated fields must be limited to a safe allowlist (no logic, no
  loops): `{{ output }}`, `{{ output.truncated N }}`, `{{ run.id }}`,
  `{{ run.date }}`, `{{ run.started_at }}`, `{{ schedule.name }}`,
  `{{ skill.name }}`.

### FR-5: Learnings write-back

- FR-5.1 Skills may declare `writeback: { path, mode }` in YAML config.
- FR-5.2 The runner must parse a reserved `<memory>...</memory>` block from
  the model's output and commit the contents to the configured path on the
  skill repo's default branch, with bot identity `cronfoundry[bot]`.
- FR-5.3 Mode `append` concatenates to existing file content; mode `replace`
  overwrites.
- FR-5.4 Write-back failure (commit conflict, missing permission) must not
  prevent destination publishing; the run is marked `partial_failure`.
- FR-5.5 Write-back is strictly opt-in per skill; default is disabled.

### FR-6: Secrets management

- FR-6.1 All secrets (LLM keys, webhook URLs, per-skill env vars, GitHub App
  private key) must live in Azure Key Vault.
- FR-6.2 Secret values must never be stored in the application database, logs,
  or any UI response after creation; only metadata (name, last-updated,
  last-used) is visible post-creation.
- FR-6.3 Secret rotation must be a paste-new-value UI operation, preserving
  history via Key Vault versioning.
- FR-6.4 Each run must operate under a **scoped secret manifest** — the list
  of KV refs it is permitted to read — enforced at minimum via audit logging
  (cryptographic enforcement deferred).
- FR-6.5 Every secret access must be auditable by run ID.

### FR-7: Authentication & authorization

- FR-7.1 The UI must authenticate users via GitHub OAuth, reusing the
  CronFoundry GitHub App's OAuth client.
- FR-7.2 Access must be restricted to an allowlist of GitHub logins and/or
  org slugs, configurable by the self-hoster.
- FR-7.3 Two roles: `admin` (full write) and `viewer` (read-only dashboard).
- FR-7.4 Every mutating action (connect repo, create secret, rotate secret,
  trigger run, pause schedule) must be recorded in an audit log with the
  actor's GitHub login.

### FR-8: Observability

- FR-8.1 Every run must produce a structured record: status, timing, token
  counts, cost estimate, destination results, write-back commit SHA.
- FR-8.2 The UI must display recent run history per schedule, with drill-down
  to per-event timeline and log tail.
- FR-8.3 Live-tail of logs for in-flight runs must be available via the UI.
- FR-8.4 Run-level metrics must be emitted as OpenTelemetry counters/gauges
  suitable for Azure Monitor scraping.

### FR-9: Deployability

- FR-9.1 A complete Azure deployment must be expressible as a single Bicep
  template consumed by one `az deployment` command.
- FR-9.2 Published container images (GHCR) must be pullable by a standard
  Azure subscription without private registry configuration.
- FR-9.3 Version upgrades must be performable by bumping image tags and
  re-running deployment; database migrations run automatically at API
  startup.
- FR-9.4 A self-hoster must be able to deploy + complete first-run success
  without reading source code.

## Non-Functional Requirements

### NFR-1: Reliability

- NFR-1.1 Scheduler tick cadence ≤ 60s; drift alerting at > 5 min.
- NFR-1.2 A stuck or crashed runner must not block future fires of the same
  schedule; the orphan sweep reclaims runs past their timeout + grace.
- NFR-1.3 Target run success rate > 99% excluding user-config errors.

### NFR-2: Security

- NFR-2.1 Principal separation: three distinct managed identities with
  least-privilege role assignments (API, scheduler, runner).
- NFR-2.2 No persistent filesystem in the runner; ephemeral `/work` only.
- NFR-2.3 All inter-service traffic stays inside the Container Apps VNet;
  only the API/UI is publicly exposed.
- NFR-2.4 CSRF protection on all mutating endpoints.
- NFR-2.5 Session cookies: HttpOnly, Secure, SameSite=Lax, 7-day idle timeout.

### NFR-3: Operability

- NFR-3.1 A single operator must be able to run the system without dedicated
  staff — no cluster upkeep, no node patching, no certificate rotation beyond
  what Azure manages automatically.
- NFR-3.2 Upgrade path must not require database downtime for minor-version
  upgrades.
- NFR-3.3 Documented runbook for: onboarding a new user, rotating the GitHub
  App, rotating user secrets, pausing all schedules (circuit-break), viewing
  audit log.

### NFR-4: Cost

- NFR-4.1 Idle-baseline cost < $90/month on Azure for a small deploy.
- NFR-4.2 Per-run overhead (excluding LLM) < $0.01 for a typical 2-minute
  skill.
- NFR-4.3 No per-user or per-run licensing cost; CronFoundry is free /
  open-source.

### NFR-5: Portability

- NFR-5.1 Cloud-specific concerns (job dispatch, secrets, identity) must be
  encapsulated behind an abstraction boundary so later AWS / GCP adapters
  slot in without service-layer changes.
- NFR-5.2 LLM providers, destinations, and GitHub integration must be
  cloud-agnostic from MVP.

## Release Plan

### MVP (first release)

Everything in the "Functional Requirements" and "Non-Functional Requirements"
sections above. No MCP tools. No Copilot Enterprise. No UI-managed schedules.
No SSO beyond GitHub. Single-tenant only (data model is tenant-aware).

### Fast-Follow Releases (prioritized order)

1. **GitHub Copilot Enterprise provider** — OAuth device flow, short-lived
   token refresh, org-seat enforcement.
2. **MCP tool support in skills** — skill frontmatter declares allowed MCP
   servers; runner boots them in-job; tool-call loop. Unlocks UC-3 and
   similar.
3. **Auto-pause on consecutive failures** — configurable threshold; surfaces
   as a dashboard badge.
4. **Rich destination formatting** — Block Kit, full Adaptive Cards,
   multi-output skills (e.g., skill emits both Slack and GitHub bodies).
5. **Conditional destination routing** — `on_failure:` / `on_success:`.
6. **UI-managed schedules** — per-skill `managed: yaml | ui`, CRUD in UI for
   UI-managed ones.
7. **SSO beyond GitHub** — Entra ID and generic OIDC providers.
8. **Additional destinations** — email, PagerDuty, custom HTTP.
9. **KV-proxy sidecar** — cryptographic enforcement of per-run secret
   manifests.
10. **Helm / AKS deploy path** — for shops already on Kubernetes.
11. **Multi-cloud** — AWS + GCP adapters for job dispatch, secrets, identity.
12. **Hosted multi-tenant SaaS** — signup, billing, per-tenant quotas.
13. **Image signing / SBOM** — supply chain hardening.

## Risks & Open Questions

### Technical risks

- **LLM provider rate limits and costs are user-visible failures.** Clear
  per-run cost display and retry semantics are baked into the MVP, but
  surprises are still possible. Mitigation: per-schedule cost ceilings as a
  fast-follow.
- **GitHub App-based integration puts the self-hoster in the critical path
  for a GitHub registration step.** Mitigation: detailed walkthrough + a
  helper CLI (`cronfoundry-bootstrap`) as a fast-follow if friction is real.
- **Azure Container Apps Jobs cold start may add 5-15s of latency per fire.**
  Tolerable for daily / weekly schedules; documented in the operator guide.
  If minute-level schedules become common, an "always-warm pool" mode is a
  post-MVP option.

### Product risks

- **"Scheduled LLM runs" may not yet be a felt need for most teams.** The
  primary bet is that it becomes one over the next year. If not, CronFoundry
  remains useful for the Platform Engineers who already have the shape of
  this problem.
- **Competing tools (Claude routines, future ChatGPT "tasks") may close the
  gap.** CronFoundry's differentiators — GitOps, multi-provider, self-hosted,
  Key-Vault-grade secret handling, team-visible destinations, repo-committed
  learnings — are unlikely to be matched by vendor-specific offerings in the
  near term, but should be revisited at each major release.
- **"Self-hosted only" limits reach in the short term.** The hosted
  multi-tenant path (deferred) opens the larger audience; the MVP
  intentionally proves the concept on the smaller audience first.

### Open questions for post-MVP feedback

- Do users actually want UI-authored schedules, or is "YAML + PR" enough?
  (Drives priority of deferred item 6.)
- Is Copilot Enterprise a commonly-requested provider, or does OpenAI +
  Anthropic cover the vast majority? (Drives priority of deferred item 1.)
- What's the shape of "failed skill" triage people actually do? (Drives the
  post-MVP observability and alerting roadmap.)
- Is Teams adoption strong enough to justify maintaining the Power Automate
  path, or does `github-issue` + email cover it? (May simplify destination
  surface.)

## References

- Technical design: [`2026-04-19-cronfoundry-design.md`](./2026-04-19-cronfoundry-design.md)
- Microsoft Teams webhook deprecation:
  `https://learn.microsoft.com/microsoftteams/platform/webhooks-and-connectors/whats-new`
  (Power Automate flow path is the supported replacement)
- GitHub App docs (App manifest, installation tokens, webhooks):
  `https://docs.github.com/apps`
- Azure Container Apps Jobs:
  `https://learn.microsoft.com/azure/container-apps/jobs`
