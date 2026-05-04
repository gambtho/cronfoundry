# GitHub Pages Tone Pass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Update `docs/index.html` to (a) correct install-time copy from "afternoon" to "15 minutes", (b) reframe team-first voice as individual-first/scales-to-team, and (c) slim the comparison table from 5×8 to 4×5 while adding OpenClaw.

**Architecture:** Single-file static HTML edits using existing Tailwind utility classes. No new CSS, no new assets, no new pages. Three commits, one per concern, on branch `worktree-gh-pages-tone-pass`. Spec: `docs/superpowers/specs/2026-05-04-gh-pages-tone-pass-design.md`.

**Tech Stack:** Static HTML + Tailwind CDN classes (no build step for `docs/index.html`).

**Verification approach:** No automated tests for static HTML copy. Each task ends with grep checks (negative assertions: forbidden strings absent) and a visual instruction. Final verification is opening `docs/index.html` in a browser.

---

## File Structure

Only one production file is touched:

- **Modify:** `docs/index.html` — landing page (currently 566 lines).

No new files. No deletions. Sibling files (`docs/install.sh`, `docs/assets/og-image.html`, `docs/guides/**`) are out of scope.

---

## Task 1: Install-time copy ("afternoon" → "15 minutes")

**Files:**
- Modify: `docs/index.html` (hero badge line ~105, hero CTA line ~127, quick-start heading line ~337, quick-start code block lines ~368–379, plus left-column step prose ~341–363)

This task changes only language and the quick-start code block. Hero install line (`curl|bash` on line ~117) is intentionally **not** changed — it must stay zero-context for landing-page visitors.

- [ ] **Step 1: Update the hero badge text**

Find (around line 105):

```html
        v0.7.6 &middot; 4 LLM providers &middot; deployable to Azure today
```

Replace with:

```html
        v0.7.6 &middot; 4 LLM providers &middot; self-hosted &middot; open source
```

- [ ] **Step 2: Update the primary hero CTA button**

Find (around line 127):

```html
          Deploy in one afternoon &rarr;
```

Replace with:

```html
          Deploy in 15 minutes &rarr;
```

- [ ] **Step 3: Update the quick-start section heading**

Find (around line 337):

```html
        <h2 class="text-center text-3xl font-bold text-ink mb-3">Up and running in one afternoon</h2>
```

Replace with:

```html
        <h2 class="text-center text-3xl font-bold text-ink mb-3">Up and running in ~15 minutes</h2>
```

- [ ] **Step 4: Replace the quick-start left-column step prose**

This is the ordered list from line ~341 through ~363. Find the entire `<ol class="space-y-5">…</ol>` block and replace its three `<li>` items so that step 1 is "Clone the repo", step 2 stays "Pick a provider", and step 3 becomes "Run quickstart-copilot.sh". This pairs with the simplified code block in step 5 below.

Find:

```html
            <ol class="space-y-5">
              <li class="flex gap-4">
                <span class="flex-shrink-0 inline-flex items-center justify-center w-7 h-7 rounded border border-accent-green-dim bg-accent-green-dim font-mono text-[11px] font-medium text-[#dffbe9]">1</span>
                <div>
                  <p class="text-ink font-medium">Build the runner binary</p>
                  <p class="text-[13px] text-ink-2 mt-1">A single ~25 MB static binary. No runtime dependencies.</p>
                </div>
              </li>
              <li class="flex gap-4">
                <span class="flex-shrink-0 inline-flex items-center justify-center w-7 h-7 rounded border border-accent-green-dim bg-accent-green-dim font-mono text-[11px] font-medium text-[#dffbe9]">2</span>
                <div>
                  <p class="text-ink font-medium">Pick a provider</p>
                  <p class="text-[13px] text-ink-2 mt-1"><span class="text-ink">Using Copilot Enterprise?</span> No key needed &mdash; the runner uses your seat. <span class="text-ink">Bring your own key?</span> Export <code class="font-mono text-[12px]">OPENAI_API_KEY</code> (or the Anthropic / Azure AI Foundry equivalent).</p>
                </div>
              </li>
              <li class="flex gap-4">
                <span class="flex-shrink-0 inline-flex items-center justify-center w-7 h-7 rounded border border-accent-green-dim bg-accent-green-dim font-mono text-[11px] font-medium text-[#dffbe9]">3</span>
                <div>
                  <p class="text-ink font-medium">Run against the bundled fixture</p>
                  <p class="text-[13px] text-ink-2 mt-1">Streams a real completion. Publishes to your Slack. Commits learnings.</p>
                </div>
              </li>
            </ol>
```

Replace with:

```html
            <ol class="space-y-5">
              <li class="flex gap-4">
                <span class="flex-shrink-0 inline-flex items-center justify-center w-7 h-7 rounded border border-accent-green-dim bg-accent-green-dim font-mono text-[11px] font-medium text-[#dffbe9]">1</span>
                <div>
                  <p class="text-ink font-medium">Clone the repo</p>
                  <p class="text-[13px] text-ink-2 mt-1">One <code class="font-mono text-[12px]">git clone</code>. No runtime dependencies beyond Docker and the Azure CLI.</p>
                </div>
              </li>
              <li class="flex gap-4">
                <span class="flex-shrink-0 inline-flex items-center justify-center w-7 h-7 rounded border border-accent-green-dim bg-accent-green-dim font-mono text-[11px] font-medium text-[#dffbe9]">2</span>
                <div>
                  <p class="text-ink font-medium">Pick a provider</p>
                  <p class="text-[13px] text-ink-2 mt-1"><span class="text-ink">Using Copilot Enterprise?</span> No key needed &mdash; the script uses your seat. <span class="text-ink">Bring your own key?</span> Export <code class="font-mono text-[12px]">OPENAI_API_KEY</code> (or the Anthropic / Azure AI Foundry equivalent).</p>
                </div>
              </li>
              <li class="flex gap-4">
                <span class="flex-shrink-0 inline-flex items-center justify-center w-7 h-7 rounded border border-accent-green-dim bg-accent-green-dim font-mono text-[11px] font-medium text-[#dffbe9]">3</span>
                <div>
                  <p class="text-ink font-medium">Run <code class="font-mono text-[12px]">quickstart-copilot.sh</code></p>
                  <p class="text-[13px] text-ink-2 mt-1">One script. Provisions Azure, builds the image, runs your first skill end-to-end.</p>
                </div>
              </li>
            </ol>
```

- [ ] **Step 5: Replace the quick-start code block**

Find (around lines 368–379):

```html
          <div class="rounded border border-rule bg-bg-2 p-5 font-mono text-[13px] text-ink">
            <p class="text-ink-3 mb-1"># 1. Build</p>
            <p class="mb-4"><span class="text-ink-3">$ </span>go build -o cronfoundry-runner ./cmd/runner</p>
            <p class="text-ink-3 mb-1"># 2. Pick a provider (skip if using Copilot Enterprise)</p>
            <p class="mb-4"><span class="text-ink-3">$ </span>export OPENAI_API_KEY='sk-...'</p>
            <p class="text-ink-3 mb-1"># 3. Run smoke fixture</p>
            <p><span class="text-ink-3">$ </span>./cronfoundry-runner run \</p>
            <p class="pl-4">--repo ./testdata \</p>
            <p class="pl-4">--skill-path skills/weekly-digest \</p>
            <p class="pl-4">--schedule-name monday-morning \</p>
            <p class="pl-4">--dry-run</p>
          </div>
```

Replace with:

```html
          <div class="rounded border border-rule bg-bg-2 p-5 font-mono text-[13px] text-ink">
            <p class="text-ink-3 mb-1"># 1. Clone</p>
            <p class="mb-1"><span class="text-ink-3">$ </span>git clone https://github.com/gambtho/cronfoundry.git</p>
            <p class="mb-4"><span class="text-ink-3">$ </span>cd cronfoundry</p>
            <p class="text-ink-3 mb-1"># 2. Pick a provider (skip if using Copilot Enterprise)</p>
            <p class="mb-4"><span class="text-ink-3">$ </span>export OPENAI_API_KEY='sk-...'</p>
            <p class="text-ink-3 mb-1"># 3. Run the Copilot Enterprise quick-start</p>
            <p><span class="text-ink-3">$ </span>bash scripts/quickstart-copilot.sh</p>
          </div>
```

- [ ] **Step 6: Verify the install-time copy**

Run from repo root:

```bash
grep -n "afternoon" docs/index.html
```

Expected output: empty (no matches). If any match remains, find and fix it before committing.

```bash
grep -nE "Deploy in 15 minutes|Up and running in ~15 minutes|quickstart-copilot\.sh|self-hosted &middot; open source" docs/index.html
```

Expected: 4+ matches (the badge, the CTA, the H2, and the script reference each at least once).

- [ ] **Step 7: Commit**

```bash
git add docs/index.html
git commit -m 'docs(gh-pages): correct install-time copy from "afternoon" to "15 minutes"'
```

---

## Task 2: Reframe team-first voice → individual-first, scales-to-team

**Files:**
- Modify: `docs/index.html` (head metadata lines 6–16, H1 lines 107–110, hero subhead lines 111–115, problem card #2 line 163, how-it-works subhead line 177, use cases subhead line 223, use cases grid lines 224–243)

- [ ] **Step 1: Update the page title and OG/Twitter titles**

Find (line 6):

```html
  <title>CronFoundry — Scheduled LLM skills for your team</title>
```

Replace with:

```html
  <title>CronFoundry — Self-hosted scheduled LLM skills</title>
```

Find (line 8):

```html
  <meta property="og:title" content="CronFoundry — Scheduled LLM skills for your team" />
```

Replace with:

```html
  <meta property="og:title" content="CronFoundry — Self-hosted scheduled LLM skills" />
```

Find (line 15):

```html
  <meta name="twitter:title" content="CronFoundry — Scheduled LLM skills for your team" />
```

Replace with:

```html
  <meta name="twitter:title" content="CronFoundry — Self-hosted scheduled LLM skills" />
```

Meta description and OG description (lines 7, 9, 16, 17) are intentionally unchanged.

- [ ] **Step 2: Update the H1**

Find (lines 107–110):

```html
      <h1 class="text-5xl sm:text-6xl font-bold tracking-tight text-ink leading-[1.05] mb-6">
        Scheduled LLM skills.<br />
        In git. For your whole team.
      </h1>
```

Replace with:

```html
      <h1 class="text-5xl sm:text-6xl font-bold tracking-tight text-ink leading-[1.05] mb-6">
        Scheduled LLM skills.<br />
        In git. Yours to run.
      </h1>
```

- [ ] **Step 3: Update the hero subhead**

Find (lines 111–115):

```html
      <p class="max-w-2xl mx-auto text-lg text-ink-2 mb-10">
        CronFoundry runs AI skills on a cron, publishes results to Slack, GitHub, Discord, or Teams,
        and commits learnings back to your repo.
        <span class="text-ink">Self-hosted. GitOps-native. BYOK.</span>
      </p>
```

Replace with:

```html
      <p class="max-w-2xl mx-auto text-lg text-ink-2 mb-10">
        Run AI on a cron. Publish results to Slack, GitHub, Discord, or Teams. Keep your prompts and keys in your repo &mdash; solo today, your whole team tomorrow.
        <span class="text-ink">Self-hosted. GitOps-native. BYOK.</span>
      </p>
```

- [ ] **Step 4: Update problem card #2 ("Reinventing the wheel")**

Find (around line 163):

```html
            <p class="text-[13px] text-ink-2">Generic cron + shell scripts work, but every team rebuilds prompt management, secret handling, output formatting, and observability from scratch.</p>
```

Replace with:

```html
            <p class="text-[13px] text-ink-2">Generic cron + shell scripts work, but you end up rebuilding prompt management, secret handling, output formatting, and observability from scratch.</p>
```

- [ ] **Step 5: Update the "How it works" subhead**

Find (around line 177):

```html
      <p class="text-center text-ink-2 mb-16 max-w-xl mx-auto">Three steps from config to team-visible output.</p>
```

Replace with:

```html
      <p class="text-center text-ink-2 mb-16 max-w-xl mx-auto">Three steps from config to scheduled output.</p>
```

- [ ] **Step 6: Update the "Use cases" subhead**

Find (around line 223):

```html
        <p class="text-center text-ink-2 mb-16 max-w-xl mx-auto">What teams are already building with CronFoundry.</p>
```

Replace with:

```html
        <p class="text-center text-ink-2 mb-16 max-w-xl mx-auto">What people are already building with CronFoundry.</p>
```

- [ ] **Step 7: Add a 4th use-case card and adjust the grid**

Find (line 224):

```html
        <div class="grid sm:grid-cols-3 gap-4">
```

Replace with:

```html
        <div class="grid sm:grid-cols-2 lg:grid-cols-4 gap-4">
```

Then, immediately before the closing `</div>` of the grid (which is the `</div>` directly after the third card "Backlog grooming", around line 242), insert this new fourth card:

```html
          <div class="rounded border border-rule bg-bg-2 p-6">
            <div class="text-3xl mb-3">&#x1F305;</div>
            <div class="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-accent-green mb-2">Daily brief</div>
            <h3 class="font-semibold text-ink mb-2">Personal morning brief</h3>
            <p class="text-[13px] text-ink-2">Scans your calendar, PR queue, and unread issues, and DMs you a structured Slack summary at 7:55am every weekday &mdash; just for you.</p>
          </div>
```

(`&#x1F305;` is the sunrise emoji 🌅, distinct from the existing 📋, 🚨, 🔍 in the other three cards.)

- [ ] **Step 8: Verify the reframe**

Run:

```bash
grep -nE "for your whole team|For your whole team|team-visible|every team rebuilds|What teams are already" docs/index.html
```

Expected output: empty.

```bash
grep -nE "Yours to run|solo today, your whole team tomorrow|Personal morning brief|What people are already|Self-hosted scheduled LLM skills" docs/index.html
```

Expected: 5 matches (one per pattern).

- [ ] **Step 9: Commit**

```bash
git add docs/index.html
git commit -m "docs(gh-pages): reframe team-first copy as individual-first, scales-to-team"
```

---

## Task 3: Slim comparison table; add OpenClaw; collapse roadmap "done" cards

**Files:**
- Modify: `docs/index.html` (comparison section lines ~247–332, roadmap "done" grid lines ~388–409)

- [ ] **Step 1: Replace the comparison table body**

The table currently has 5 columns (CronFoundry, gh-aw, Claude Routines, n8n/Zapier, cron+scripts) and 8 rows. We replace it with 4 columns (CronFoundry, gh-aw, Claude Routines, OpenClaw) and 5 rows, with a new top row "Trigger model".

Find the entire `<div class="overflow-x-auto rounded border border-rule">…</div>` block (starts around line 251, contains the `<table>`). Replace it with:

```html
      <div class="overflow-x-auto rounded border border-rule">
        <table class="w-full text-[13px]">
          <thead>
            <tr class="border-b border-rule bg-bg-3 font-mono text-[10px] uppercase tracking-[0.16em]">
              <th class="px-4 py-2.5 text-left text-ink-2 font-medium"></th>
              <th class="px-4 py-2.5 text-left text-ink font-medium">CronFoundry</th>
              <th class="px-4 py-2.5 text-left text-ink-2 font-medium">gh&#8209;aw</th>
              <th class="px-4 py-2.5 text-left text-ink-2 font-medium">Claude Routines</th>
              <th class="px-4 py-2.5 text-left text-ink-2 font-medium">OpenClaw</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-rule bg-bg-2">
            <tr>
              <td class="px-4 py-3 text-ink-2 font-medium">Trigger model</td>
              <td class="px-4 py-3 text-ink">Cron schedule</td>
              <td class="px-4 py-3 text-ink-2">GitHub events</td>
              <td class="px-4 py-3 text-ink-2">Cron schedule</td>
              <td class="px-4 py-3 text-ink-2">Chat / on-demand</td>
            </tr>
            <tr>
              <td class="px-4 py-3 text-ink-2 font-medium">Config lives in</td>
              <td class="px-4 py-3 text-ink">Git (YAML)</td>
              <td class="px-4 py-3 text-ink-2">Git (Markdown)</td>
              <td class="px-4 py-3 text-ink-2">Vendor UI</td>
              <td class="px-4 py-3 text-ink-2">Local JSON</td>
            </tr>
            <tr>
              <td class="px-4 py-3 text-ink-2 font-medium">Runs on</td>
              <td class="px-4 py-3 text-ink">Self-hosted (Azure today; AKS, Fly.io next)</td>
              <td class="px-4 py-3 text-ink-2">GitHub Actions</td>
              <td class="px-4 py-3 text-ink-2">Anthropic cloud</td>
              <td class="px-4 py-3 text-ink-2">Your laptop / SSH</td>
            </tr>
            <tr>
              <td class="px-4 py-3 text-ink-2 font-medium">LLM providers</td>
              <td class="px-4 py-3 text-ink">OpenAI, Anthropic, Azure, Copilot Enterprise</td>
              <td class="px-4 py-3 text-ink-2">Copilot, Claude, Codex</td>
              <td class="px-4 py-3 text-ink-2">Anthropic only</td>
              <td class="px-4 py-3 text-ink-2">BYOK (multi)</td>
            </tr>
            <tr>
              <td class="px-4 py-3 text-ink-2 font-medium">Best for</td>
              <td class="px-4 py-3 text-ink">Self-hosted scheduled AI for you or your team</td>
              <td class="px-4 py-3 text-ink-2">Repo automation</td>
              <td class="px-4 py-3 text-ink-2">Personal Anthropic schedules</td>
              <td class="px-4 py-3 text-ink-2">Personal chat-driven assistant</td>
            </tr>
          </tbody>
        </table>
      </div>
```

- [ ] **Step 2: Replace the comparison footnote**

Find (around line 331):

```html
      <p class="text-center text-xs text-ink-3 mt-4">gh-aw and CronFoundry are complementary &mdash; gh-aw improves your repo automatically; CronFoundry runs skills and tells your team.</p>
```

Replace with:

```html
      <p class="text-center text-xs text-ink-3 mt-4 max-w-3xl mx-auto">gh-aw is complementary &mdash; runs on repo events, not the clock. OpenClaw answers chat messages; CronFoundry runs on a schedule. Already happy with n8n, Zapier, or your own cron jobs? Stick with them &mdash; CronFoundry is for people who want LLM-aware scheduling without building the prompt/secret/output plumbing themselves.</p>
```

- [ ] **Step 3: Collapse the roadmap "done" cards**

Find the roadmap "done" grid (around lines 388–409). It is the 4-column grid of green P1/P2/P3/P4 cards. Find:

```html
      <div class="grid sm:grid-cols-4 gap-3 mb-12">
        <div class="rounded border border-accent-green-dim bg-accent-green/[0.05] p-5">
          <div class="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-accent-green mb-2">P1 &middot; Done &#x2713;</div>
          <h3 class="font-semibold text-ink mb-2">Core runner</h3>
          <p class="text-xs text-ink-2">Single binary, all LLM providers, all destinations, writeback, full test suite.</p>
        </div>
        <div class="rounded border border-accent-green-dim bg-accent-green/[0.05] p-5">
          <div class="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-accent-green mb-2">P2 &middot; Done &#x2713;</div>
          <h3 class="font-semibold text-ink mb-2">Scheduler + API</h3>
          <p class="text-xs text-ink-2">Postgres, cron scheduler, REST API, Azure Key Vault integration.</p>
        </div>
        <div class="rounded border border-accent-green-dim bg-accent-green/[0.05] p-5">
          <div class="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-accent-green mb-2">P3 &middot; Done &#x2713;</div>
          <h3 class="font-semibold text-ink mb-2">Web UI</h3>
          <p class="text-xs text-ink-2">React dashboard, GitHub OAuth, secret CRUD, run history, live log tail.</p>
        </div>
        <div class="rounded border border-accent-green-dim bg-accent-green/[0.05] p-5">
          <div class="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-accent-green mb-2">P4 &middot; Done &#x2713;</div>
          <h3 class="font-semibold text-ink mb-2">Azure deploy</h3>
          <p class="text-xs text-ink-2">Bicep template, GHCR images, one-command deploy from empty subscription.</p>
        </div>
      </div>
```

Replace with:

```html
      <div class="mb-12 flex justify-center">
        <div class="rounded border border-accent-green-dim bg-accent-green/[0.05] px-5 py-4 text-center max-w-2xl">
          <div class="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-accent-green mb-2">MVP shipped &middot; P1&ndash;P4 &#x2713;</div>
          <p class="text-[13px] text-ink-2">Core runner &middot; Scheduler + API &middot; Web UI &middot; Azure deploy</p>
        </div>
      </div>
```

- [ ] **Step 4: Verify the comparison and roadmap changes**

Run:

```bash
grep -nE "n8n / Zapier|cron \+ scripts|Self-hostable|gh-aw and CronFoundry are complementary" docs/index.html
```

Expected output: empty.

```bash
grep -nE "OpenClaw|Trigger model|Chat / on-demand|MVP shipped &middot; P1&ndash;P4|gh-aw is complementary" docs/index.html
```

Expected: 5+ matches (OpenClaw appears in header + body, plus the four other patterns).

```bash
grep -c "<th " docs/index.html
```

Expected: `5` (1 empty corner + 4 product columns).

- [ ] **Step 5: Visual sanity check**

Open `docs/index.html` in a browser (or via VS Code's Live Preview / `python3 -m http.server`). Confirm:
- Hero says "Yours to run", badge shows "self-hosted · open source", CTA shows "Deploy in 15 minutes".
- Use cases section shows 4 cards on a wide screen, 2×2 on tablet.
- Comparison table has exactly 4 product columns (CronFoundry, gh-aw, Claude Routines, OpenClaw) and 5 data rows; no horizontal scroll at 1280px width.
- Roadmap shows a single centered "MVP shipped · P1–P4 ✓" badge, followed by the existing "What's next" chips (unchanged).

- [ ] **Step 6: Commit**

```bash
git add docs/index.html
git commit -m "docs(gh-pages): slim comparison table; add OpenClaw; collapse roadmap done cards"
```

---

## Task 4: Push branch and open PR

- [ ] **Step 1: Push the branch**

```bash
git push -u origin worktree-gh-pages-tone-pass
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --title "docs(gh-pages): tone pass — install time, individual voice, slimmer comparison" --body "$(cat <<'EOF'
## Summary
- Hero/quick-start copy: replaced "one afternoon" with "~15 minutes" and "Deploy in 15 minutes" CTA; quick-start code block now mirrors the dogfood prompt (`git clone` + `bash scripts/quickstart-copilot.sh`).
- Voice: reframed team-first language as individual-first/scales-to-team. New H1 ("In git. Yours to run."), updated subheads, problem-card #2, and a new 4th use-case card ("Personal morning brief").
- Comparison: slimmed from 5×8 to 4×5; dropped n8n/Zapier and cron+scripts columns plus three rows; added OpenClaw column and a new "Trigger model" top row.
- Roadmap: collapsed the four identical green "Done" cards to a single "MVP shipped · P1–P4 ✓" badge.

Spec: `docs/superpowers/specs/2026-05-04-gh-pages-tone-pass-design.md`
Plan: `docs/superpowers/plans/2026-05-04-gh-pages-tone-pass.md`

## Test plan
- [ ] Open `docs/index.html` locally; confirm hero, use cases (4 cards), comparison (4 product columns), and roadmap (single badge) render correctly.
- [ ] At 1280px viewport width, confirm the comparison table does not horizontally scroll.
- [ ] Confirm `grep -n afternoon docs/index.html` returns nothing.
- [ ] Confirm `grep -n "for your whole team" docs/index.html` returns nothing.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Report PR URL to user**

The `gh pr create` output ends with the PR URL. Surface it in a final message.

---

## Self-review notes

- **Spec coverage:** Each numbered section of the spec maps to at least one task: §1 hero → Task 1 + Task 2 step 1–3; §2 problem → Task 2 step 4; §3 how it works → Task 2 step 5; §4 use cases → Task 2 steps 6–7; §5 quick-start → Task 1 steps 3–5; §6 comparison → Task 3 steps 1–2; §7 roadmap → Task 3 step 3. PR delivery → Task 4.
- **Placeholder scan:** No "TBD"/"TODO"/"add appropriate handling" in any step. Each code block contains the literal HTML to paste.
- **Type/string consistency:** All grep verification strings match the exact output strings written elsewhere in the same task (e.g., "Yours to run", "MVP shipped &middot; P1&ndash;P4", "Self-hosted scheduled LLM skills"). Use-case card emoji `&#x1F305;` is verified distinct from the existing `&#x1F4CB;` / `&#x1F6A8;` / `&#x1F50D;`.
