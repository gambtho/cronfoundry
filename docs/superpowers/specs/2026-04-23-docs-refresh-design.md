# CronFoundry Docs Refresh — Design

**Status:** Proposed  
**Date:** 2026-04-23  
**Author:** gambtho (brainstormed with Claude)

## Overview

Three deliverables that together take a first-time user from the landing page to a running
CronFoundry deployment on Azure in one session, using GitHub Copilot Enterprise as the LLM
provider (no external API key required).

1. **`scripts/quickstart-copilot.sh`** — interactive bash script that automates everything
   automatable in the Azure deploy path, with the Copilot Enterprise device flow as the LLM
   setup step.

2. **`docs/guides/quickstart-copilot.md`** — full prose companion to the script. Documents
   every step the script performs so users who hit failures mid-run or want to understand what
   happened have a reference. Same section order as the script.

3. **`docs/index.html` refresh** — update the hero badge and features section to reflect
   post-MVP capabilities; add a prominent Copilot quick-start CTA with the `curl | bash`
   one-liner.

---

## Deliverable 1: `scripts/quickstart-copilot.sh`

### Hosting

The script is committed at `scripts/quickstart-copilot.sh` and symlinked (or copied) to
`docs/install.sh` so GitHub Pages serves it at
`https://gambtho.github.io/cronfoundry/install.sh`. The landing page one-liner becomes:

```bash
bash <(curl -fsSL https://gambtho.github.io/cronfoundry/install.sh)
```

### Script flow (17 steps)

| Step | Action |
|------|--------|
| 1 | **Prereq check** — verify `az` ≥ 2.60, Bicep ≥ 0.26, `git`, `python3`, `openssl` present; print install hint for any missing tool and exit |
| 2 | **az login** — run `az account show`; if already logged in print identity and skip |
| 3 | **Subscription** — `az account list`, prompt user to pick by number |
| 4 | **Clone check** — verify `$PWD` is inside a cronfoundry git clone (check for `deploy/main.bicep`); if not, print clone instructions and exit |
| 5 | **GitHub App** — print URL (`https://github.com/settings/apps/new`), list required permissions/events, then prompt for App ID, Client ID, Client Secret, and PEM file path |
| 6 | **Skill repo** — prompt for `owner/skill-repo` and installation ID |
| 7 | **Reports repo** — prompt for `owner/reports-repo` |
| 8 | **Master key** — generate with `openssl rand -base64 32`; print to terminal and save to state file |
| 9 | **Env suffix** — prompt (default: `copilot1`); warn that Key Vault soft-delete retains the name for 7 days, so re-runs need a new suffix |
| 10 | **Region** — default `swedencentral`; note it is the proven region for Microsoft-internal subs; allow override |
| 11 | **Image tag** — query `https://ghcr.io/v2/gambtho/cronfoundry/tags/list` for the latest semver tag; fall back to `latest` |
| 12 | **Postgres password** — generate 24-char alphanumeric with `openssl rand -base64 18 \| tr -dc A-Za-z0-9 \| head -c24` |
| 13 | **Build params** — write `deploy/params.quickstart.json` using Python (same technique as the smoke runbook for PEM embedding) |
| 14 | **Deploy** — `az deployment sub create`; stream output; wait; extract FQDN on completion |
| 15 | **admin init** — add `0.0.0.0–255.255.255.255` Postgres firewall rule (WSL2-safe), run `cronfoundry admin init`, force Container App revision, poll until `Healthy` |
| 16 | **Print FQDN** — tell user to update GitHub App Homepage URL, Callback URL, and Webhook URL |
| 17 | **UI checklist** — print numbered next steps: (a) open `https://<fqdn>/`, (b) Providers → GitHub Copilot Enterprise → Connect (device flow), (c) Repos → Connect repo, (d) Secrets → Add `github_webhook_secret`, (e) push a skill YAML using `provider: copilot-enterprise` and `copilot_prefix: <prefix>` matching the prefix entered during device flow |

### Resumability

The script writes a state file to `~/.cronfoundry-quickstart-state` (shell-sourceable key=value
pairs) after each completed step. On re-run it sources the file and skips steps whose output is
already recorded. The deploy step is idempotent (incremental Bicep redeploy); admin init checks
whether the schema already exists before migrating.

### Error handling

- `set -euo pipefail` throughout.
- Each step is wrapped in a function; failures print the step name, the failing command, and a
  pointer to the corresponding section in `docs/guides/quickstart-copilot.md`.
- The Postgres firewall rule uses the broad `0.0.0.0–255.255.255.255` range by default (matches
  the Session 2/3 WSL2 finding); a comment explains why.

### What the script cannot automate

- GitHub App creation (browser-only form).
- GitHub App installation on repos (browser-only).
- Copilot Enterprise device flow authorization (browser-only — user opens `verification_uri`
  and enters `user_code`).
- Skill YAML creation and push (user's own repo content).

These four manual steps are clearly separated in the UI checklist printed at step 17.

---

## Deliverable 2: `docs/guides/quickstart-copilot.md`

### Structure

Mirrors the script's 17 steps section-by-section, so a user who is `ctrl+F`-ing "step 9" lands
in the right place. Each section includes:

- What the script does and why.
- The raw commands the script runs (so a user can run them manually if the script fails).
- Any gotchas surfaced by the smoke tests (WSL2 NAT, Postgres offer restrictions, KV name lock,
  `--no-wait` misleading exit code, etc.).

### Relationship to existing runbooks

`smoke-test-mvp-azure.md` remains the authoritative end-to-end verification runbook (it covers
audit log verification, teardown, pass/fail checklist). The new guide is the "happy path" for a
first-time user; it does not replace the smoke test.

---

## Deliverable 3: `docs/index.html` refresh

### Hero badge

Change from:
```
MVP shipped ✓ — deployable to Azure today
```
To:
```
v0.7.6 · 4 LLM providers · deployable to Azure today
```

### Quick-start CTA

Replace the existing "Try it in 5 minutes →" button block with:

```
bash <(curl -fsSL https://gambtho.github.io/cronfoundry/install.sh)
```

displayed in the terminal mockup already on the page, with a sub-note:
"No API key needed — uses GitHub Copilot Enterprise. [Full guide →]"

The existing "Self-host on Azure in one afternoon" link below the buttons stays and updates to
point to `docs/guides/quickstart-copilot.md`.

### Features section

Add a features grid or expand the existing "Why existing tools fall short" section to mention:

- 4 LLM providers (OpenAI, Anthropic, Azure AI Foundry, GitHub Copilot Enterprise)
- MCP tool support (skills can call MCP servers)
- Conditional destination routing (`when: on_success / on_failure`)
- Rich Slack/Teams formatting (Adaptive Cards, mrkdwn blocks)
- Custom HTTP and SMTP email destinations
- Auto-pause after consecutive failures
- Multicloud deploy (Azure Container Apps, AKS, Fly.io)

These are factual additions only — no redesign of the page layout.

---

## Out of scope

- A hosted docs site (GitHub Pages multi-page navigation, search, versioning). Listed as lower
  priority; can be a follow-on.
- Updating `smoke-test-mvp-azure.md` to add Copilot as an alternative LLM path. The smoke test
  uses the API-key path for repeatability; the Copilot path is the quick-start's job.
- Windows / PowerShell variant of the script.
