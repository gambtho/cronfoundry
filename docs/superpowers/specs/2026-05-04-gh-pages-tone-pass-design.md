# GitHub Pages tone pass — design

**Date:** 2026-05-04
**Scope:** `docs/index.html` only. No new pages, no asset/CSS overhaul.
**Goal:** Fix three problems with the landing page:
1. Install effort overstated — "afternoon" reads like half a day; real install is ~15 minutes via the existing one-liner.
2. Voice leans heavily team-first; an individual developer (the realistic first user of an OSS self-hosted tool) is told "for teams" five times before reaching the install command.
3. Comparison table is too wide (5 columns × 8 rows, scrolls horizontally) and is missing OpenClaw, an adjacent tool readers will reasonably ask about.

---

## 1. Hero

**Title tag and OG title** (`docs/index.html` lines 6, 8, 15):
- From: `CronFoundry — Scheduled LLM skills for your team`
- To:   `CronFoundry — Self-hosted scheduled LLM skills`

**Meta description / OG description** (lines 7, 16): unchanged. The "Slack, GitHub, Discord, or Teams" wording is still accurate and useful for share previews.

**Hero badge** (line 105):
- From: `v0.7.6 · 4 LLM providers · deployable to Azure today`
- To:   `v0.7.6 · 4 LLM providers · self-hosted · open source`

**H1** (lines 107–110):
- From: `Scheduled LLM skills.<br />In git. For your whole team.`
- To:   `Scheduled LLM skills.<br />In git. Yours to run.`

**Hero subhead** (lines 111–115):
- From: `CronFoundry runs AI skills on a cron, publishes results to Slack, GitHub, Discord, or Teams, and commits learnings back to your repo. <span>Self-hosted. GitOps-native. BYOK.</span>`
- To:   `Run AI on a cron. Publish results to Slack, GitHub, Discord, or Teams. Keep your prompts and keys in your repo — solo today, your whole team tomorrow. <span>Self-hosted. GitOps-native. BYOK.</span>`

**Hero install line** (line 117): **unchanged.** `curl|bash` is correct for a no-context landing-page reader. (We considered swapping to `bash scripts/quickstart-copilot.sh` to match the dogfood prompt, but that form requires a prior `git clone` and is wrong for the hero. We will use it in the quick-start section instead — see §5.)

**Primary CTA button** (line 127):
- From: `Deploy in one afternoon →`
- To:   `Deploy in 15 minutes →`

---

## 2. Problem section (lines 151–172)

Single edit, card #2 ("Reinventing the wheel"), to align with the individual-first frame:
- From: `Generic cron + shell scripts work, but every team rebuilds prompt management, secret handling, output formatting, and observability from scratch.`
- To:   `Generic cron + shell scripts work, but you end up rebuilding prompt management, secret handling, output formatting, and observability from scratch.`

Cards 1 and 3 are unchanged.

---

## 3. How it works (lines 174–217)

Subhead (line 177):
- From: `Three steps from config to team-visible output.`
- To:   `Three steps from config to scheduled output.`

Step 3 paragraph (line 208) is unchanged — "publishes in parallel to GitHub issues, Slack, Discord, or Teams" already fits both audiences.

---

## 4. Use cases (lines 219–245)

Subhead (line 223):
- From: `What teams are already building with CronFoundry.`
- To:   `What people are already building with CronFoundry.`

Add a fourth card to the grid (change `grid-cols-3` → `grid-cols-2 lg:grid-cols-4` on line 224 so it lays out as 2×2 on tablet, 1×4 on desktop), framed as a solo use case so the page no longer reads as team-only:

```text
Icon: 🌅
Eyebrow: Daily brief
Title:   Personal morning brief
Body:    Scans your calendar, PR queue, and unread issues, and DMs you
         a structured Slack summary at 7:55am every weekday.
```

The three existing cards (engineering digest, on-call handoff, backlog grooming) stay as-is — they're still good team-scale examples and the new card balances them.

---

## 5. Quick-start section (lines 334–382)

Heading (line 337):
- From: `Up and running in one afternoon`
- To:   `Up and running in ~15 minutes`

Subhead (line 338) unchanged.

**Code block** (lines 368–379) — replace the three-step `go build / export / runner --dry-run` example with the dogfood-equivalent commands, which are both shorter and what we actually run ourselves:

```bash
# 1. Clone
$ git clone https://github.com/gambtho/cronfoundry.git
$ cd cronfoundry

# 2. Pick a provider (skip if using Copilot Enterprise)
$ export OPENAI_API_KEY='sk-...'

# 3. Run the Copilot Enterprise quick-start
$ bash scripts/quickstart-copilot.sh
```

The three numbered steps in the left column (lines 341–363) describe the same flow at a higher level — keep them, but tighten step 1 to "Clone the repo" and step 3 to "Run `quickstart-copilot.sh`" so the prose matches the code. Step 2 ("Pick a provider") is unchanged.

---

## 6. Comparison table (lines 247–331)

Slim from 5 columns × 8 rows to 4 columns × 5 rows. Drop the `n8n / Zapier` and `cron + scripts` columns (straw-man comparisons that don't help a reader who's already on the CronFoundry site). Drop the `Self-hostable`, `Open source`, `Secrets`, `Output destinations` rows (subsumed by `Runs on` + the new `Trigger model` row, or too in-the-weeds for a comparison). **Add an OpenClaw column** and a new top row, `Trigger model`, which is the cleanest way to position the four tools against each other:

| | **CronFoundry** | **gh-aw** | **Claude Routines** | **OpenClaw** |
|---|---|---|---|---|
| Trigger model      | Cron schedule                        | GitHub events  | Cron schedule              | Chat / on-demand                |
| Config lives in    | Git (YAML)                           | Git (Markdown) | Vendor UI                  | Local JSON                      |
| Runs on            | Self-hosted (Azure today; AKS, Fly.io next) | GitHub Actions | Anthropic cloud            | Your laptop / SSH               |
| LLM providers      | OpenAI, Anthropic, Azure, Copilot Enterprise | Copilot, Claude, Codex | Anthropic only             | BYOK (multi)                    |
| Best for           | Self-hosted scheduled AI for you or your team | Repo automation | Personal Anthropic schedules | Personal chat-driven assistant  |

Section subhead (line 250) unchanged.

Footnote (line 331) — replace with:
> *"gh-aw is complementary — runs on repo events, not the clock. OpenClaw answers chat messages; CronFoundry runs on a schedule. Already happy with n8n, Zapier, or your own cron jobs? Stick with them — CronFoundry is for people who want LLM-aware scheduling without building the prompt/secret/output plumbing themselves."*

OpenClaw facts confirmed from `openclaw.ai` and `github.com/openclaw/openclaw` (config at `~/.openclaw/openclaw.json`, local-first w/ optional SSH/Tailscale, BYOK multi-provider, MIT license, chat-driven).

---

## 7. Roadmap (lines 384–426)

The four "P1–P4 Done" cards are visually identical and take 4 columns of vertical space to say one thing. Collapse them to a single hero badge above the existing "What's next" chips:

```text
[ MVP shipped · P1–P4 ✓ ]
Core runner · Scheduler + API · Web UI · Azure deploy
```

The "What's next" chip rows (lines 411–425) are unchanged.

---

## Out of scope (deliberately)

- No CSS, font, or layout overhaul. Only HTML text + class tweaks where the grid changes.
- No new pages, no new images, no OG image regeneration.
- No changes to `web/index.html` (the app shell).
- No changes to install scripts, `install.sh`, or `scripts/quickstart-copilot.sh` themselves.
- No SEO / sitemap / robots changes.

## Verification

After implementation:
1. Open `docs/index.html` in a browser locally; confirm hero, problem, how-it-works, use cases, quick-start, comparison, and roadmap render correctly at desktop and mobile widths.
2. Confirm no broken Tailwind utility classes (visual scan; we're inside the existing design system, not adding new tokens).
3. Confirm no copy still says "afternoon" or "for your whole team".
4. Confirm comparison table fits without horizontal scroll at 1280px viewport.

## Delivery

One PR against `main`, three commits:
1. `docs(gh-pages): correct install-time copy from "afternoon" to "15 minutes"`
2. `docs(gh-pages): reframe team-first copy as individual-first, scales-to-team`
3. `docs(gh-pages): slim comparison table; add OpenClaw; collapse roadmap done cards`
