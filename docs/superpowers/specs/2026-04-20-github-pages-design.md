# CronFoundry GitHub Pages Site — Design

**Status:** Shipped (16c2af3)
**Date:** 2026-04-20  
**Author:** gambtho

## Overview

A single long-scroll landing page (`docs/index.html`) served via GitHub Pages. No build step — plain HTML + Tailwind CDN + minimal vanilla JS. Push and it's live.

**Goal funnel:** Hook with vision → drive to try → convert to star/share  
**Audience:** Open-source community broadly — engineers, contributors, evangelists  
**Tone:** Polished indie OSS (think Linear, Resend, Astro)

---

## Page Sections

### 1. Nav

- Logo (text mark: `CronFoundry`) + tagline chip
- Links: Docs (points to README), GitHub
- GitHub star badge (shields.io dynamic count)
- Sticky on scroll, minimal height

### 2. Hero

**Headline:** "Scheduled LLM skills. In git. For your whole team."

**Subline:**
> CronFoundry runs AI skills on a cron, publishes results to Slack, GitHub, Discord, or Teams, and commits learnings back to your repo. Self-hosted. GitOps-native. BYOK.

**CTAs:**
- Primary: "Try it in 5 minutes →"
- Secondary: "View on GitHub"

**Terminal animation:** Typewriter-style animation showing:
1. The `cronfoundry-runner run` command
2. Streaming LLM output lines
3. `✓ Posted to #eng-digest` success line
4. JSON run summary

Implemented with a simple `setInterval` typewriter loop in vanilla JS. No dependencies.

### 3. Problem

Three-panel horizontal layout. Each panel: icon + bold label + 2-sentence pain description.

| Panel | Label | Pain |
|---|---|---|
| 1 | Locked to one provider | Claude Routines and ChatGPT Tasks are useful, but they don't support GitOps config, multi-destination outputs, or self-hosting. Your LLM key and your prompts live in their cloud. |
| 2 | Reinventing the wheel | Generic cron + shell scripts work, but every team rebuilds prompt management, secret handling, output formatting, and observability from scratch. |
| 3 | Too general-purpose | n8n, Zapier, and Power Automate treat LLM steps as an afterthought. Config lives inside the tool, not in git. No streaming, no token accounting, no retry semantics. |

### 4. How It Works

Three-step horizontal flow with connecting arrows. Each step: numbered badge + heading + 2-sentence description + small code/config snippet.

1. **Define in YAML** — Declare your skill path, schedule, provider, and destinations in `cronfoundry.yaml`. Lives in your repo, reviewed like code, versioned in git.
2. **Runs on schedule** — CronFoundry fires the skill at the configured cron, streams a completion from your LLM provider, and parses any `<memory>` blocks from the output.
3. **Output lands everywhere** — Results are published in parallel to GitHub issues, Slack, Discord, or Teams. Learnings are committed back to the repo as `cronfoundry[bot]`.

Minimal `cronfoundry.yaml` snippet shown inline at step 1 (the `weekly-digest` example from README).

### 5. Use Cases

Three cards in a responsive grid. Each card: emoji icon + use case name + 2-sentence description + "skill: X" metadata chip.

| Card | Name | Description |
|---|---|---|
| 📋 | Weekly engineering digest | Summarizes last week's activity and posts to a GitHub issue + Slack. Next week's run has context because learnings are committed back to `memory.md`. |
| 🚨 | On-call handoff brief | Reads yesterday's alerts, generates a structured handoff, and posts an Adaptive Card to the on-call Teams channel every weekday at 08:30. |
| 🔍 | Backlog grooming | Reviews the GitHub project board, identifies stale items, and files an issue with suggested actions — no manual triage required. |

### 6. Comparison Table

Positioned as "find the right tool for your job" — honest, not combative.

| | CronFoundry | gh-aw (GitHub Next) | Claude Routines | n8n / Zapier | cron + scripts |
|---|---|---|---|---|---|
| Config lives in | Git (YAML) | Git (Markdown) | Vendor UI | Vendor UI | Shell scripts |
| Runs on | Self-hosted Azure | GitHub Actions | Vendor cloud | Vendor cloud | Your infra |
| Output destinations | Slack, Discord, Teams, GitHub | GitHub only | Email, notifications | Many | Whatever you code |
| LLM providers | OpenAI, Anthropic, Azure AI | Copilot, Claude, Codex | Anthropic only | Via plugins | Whatever you integrate |
| Secrets | Azure Key Vault | GH Actions secrets | Vendor-managed | Vendor-managed | Your choice |
| Best for | Team-visible scheduled outputs | Repo automation + improvement | Simple personal schedules | General automation | Full DIY control |
| Self-hostable | ✓ | ✗ (GH Actions) | ✗ | ✓ (complex) | ✓ |
| Open source | ✓ | ✓ (early dev) | ✗ | ✓ (community ed.) | N/A |

Caption: "gh-aw and CronFoundry are complementary — gh-aw improves your repo automatically; CronFoundry runs skills and tells your team."

### 7. Quick Start

Two-column layout: left is prose steps, right is a styled code block.

**Steps:**
1. Install the runner binary
2. Set your API key env var
3. Run against the bundled `testdata` fixture
4. See JSON run summary + output

Code block shows the exact `cronfoundry-runner run` command from the README with the `testdata` smoke fixture. "Up and running in one afternoon" framing reinforced in the prose.

CTA at bottom of section: "Ready to deploy? →" links to the deployment docs.

### 8. Roadmap

Horizontal timeline (mobile: vertical). Four phases shown as milestones:

| Phase | Label | Status | Description |
|---|---|---|---|
| P1 | Core runner | ✅ Done | Single binary, all LLM providers, all destinations, writeback |
| P2 | Scheduler + API | 🔨 In progress | Postgres, cron scheduler, REST API, Azure Key Vault |
| P3 | Web UI | 📋 Planned | React dashboard, GitHub OAuth, secret CRUD, run history |
| P4 | Azure deployment | 📋 Planned | Bicep template, GHCR images, one-command deploy |

Fast-follow items listed below as chips: MCP tool support, Copilot Enterprise, Helm/AKS, multi-cloud, hosted SaaS.

### 9. Community CTA

Dark background section (inverted). Three columns:

- **⭐ Star the repo** — "If CronFoundry solves a problem you have, a star helps others find it." → GitHub star button
- **🛠 Contribute** — "P2 and P3 are actively being built. Jump in." → GitHub issues / good-first-issue filter
- **💬 Discuss** — "Ideas, use cases, integration questions." → GitHub Discussions link

GitHub stats row: stars badge, forks badge, license badge, last commit badge — all via shields.io.

### 10. Footer

Single row: `CronFoundry · MIT License · Built with Go · GitHub`

---

## Technical Implementation

### File layout

```
docs/
├── index.html        # single-page site
├── assets/
│   ├── logo.svg      # text mark (can be SVG text, no image file needed initially)
│   └── og-image.png  # Open Graph image for sharing (1200×630)
```

### GitHub Pages config

- Pages served from `docs/` on `main` branch
- No Jekyll (add `.nojekyll` file)
- Custom domain: TBD (can add later via CNAME file)

### Tailwind

Use Tailwind CDN (`<script src="https://cdn.tailwindcss.com">`) with a small inline config block for brand colors. No build step.

**Brand colors:**
- Primary: deep indigo (`#4F46E5`) — trustworthy, technical
- Accent: emerald (`#10B981`) — success, green runs
- Surface: near-white (`#F9FAFB`) / near-black (`#111827`)

### Terminal animation

~40 lines of vanilla JS. A queue of lines is played back character-by-character using `setInterval`. No library. Replay button resets the queue.

### Open Graph / SEO

```html
<meta property="og:title" content="CronFoundry — Scheduled LLM skills for your team">
<meta property="og:description" content="Self-hosted, GitOps-native LLM skill scheduler. Runs on cron, publishes to Slack, GitHub, Discord, or Teams.">
<meta property="og:image" content="https://cronfoundry.github.io/cronfoundry/assets/og-image.png">
```

---

## Success Criteria

- First-time visitor understands what CronFoundry does within 10 seconds of landing
- Quick-start section contains a copy-pasteable command that produces a real run
- Comparison table is honest and positions gh-aw as complementary, not a competitor
- Page loads fast on mobile (no JS frameworks, no web fonts beyond system stack)
- All GitHub badges reflect live repo stats

---

## Out of Scope

- Full documentation site (deferred — use README links for now)
- Blog / changelog
- Search
- i18n
- Dark mode toggle (use system preference via `prefers-color-scheme` media query only)
