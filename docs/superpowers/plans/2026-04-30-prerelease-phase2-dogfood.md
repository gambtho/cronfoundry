# Pre-release Polish — Phase 2: Live Azure Dogfood Loop

> **For agentic workers:** This plan is a runbook, not a TDD task list. Steps describe a *procedure* to follow on a real Azure subscription. Use checkbox syntax to track progress, but expect to commit findings to a punch-list doc rather than to code.

**Goal:** Iteratively run the polished `install.sh` against a real Azure subscription until three consecutive deploys complete in <15 minutes with zero unplanned interventions and no doc lookup during the run.

**Architecture:** Operator runs `install.sh`, observes friction, commits findings to a numbered punch list, fixes them in `install.sh` / admin CLIs / Bicep, tears down, repeats. Stop after 3 rounds even if not perfect — diminishing returns.

**Tech Stack:** Real Azure subscription (~$5 per round on minimal SKUs), GitHub account with App registration permissions, Copilot Enterprise seat, two test repos.

**Spec:** `docs/superpowers/specs/2026-04-30-prerelease-polish-design.md` §Phase 2.

**Prerequisite:** Phase 1 plan (`2026-04-30-prerelease-phase1-installer.md`) is fully landed.

---

## Round 1 — Instrumented run

- [ ] **Step 1: Prep clean state**

```bash
# Pick a fresh env suffix (3 chars + digit; KV soft-delete = 7 days, so don't reuse)
export ENV_SUFFIX="round1"

# Confirm the previous state file isn't lingering
rm -f ~/.cronfoundry-quickstart-state-${ENV_SUFFIX}

# Confirm Azure subscription has no rg-cronfoundry-${ENV_SUFFIX} from a previous run
az group exists --name "rg-cronfoundry-${ENV_SUFFIX}"
# Expected: false
```

- [ ] **Step 2: Start a stopwatch and a notes file**

```bash
mkdir -p docs/superpowers/specs
NOTES="docs/superpowers/specs/$(date +%Y-%m-%d)-quickstart-dogfood-round1.md"
cat > "$NOTES" <<EOF
# Quickstart Dogfood — Round 1

Date: $(date)
Operator: $(whoami)
Env suffix: ${ENV_SUFFIX}
Start time: $(date +%H:%M:%S)

## Findings

(Numbered punch list. Each finding = one bullet. Each bullet has step number, observed behavior, expected behavior, severity (blocker/friction/polish), proposed fix.)

EOF
```

- [ ] **Step 3: Run the installer end-to-end**

```bash
bash scripts/quickstart-copilot.sh
```

While it runs, record every place where you experienced friction, in real time, in `$NOTES`. Examples of what to record:

- A prompt I had to think about for >5 seconds (means defaults are wrong, or the prompt copy is unclear)
- An error message that didn't tell me what to do next
- Output that scrolled past too fast
- A retry I had to do (means a missing wait/poll)
- A step where I had to consult a doc tab in the browser
- Wall-clock estimates that were wrong (script said "~10 min for Bicep", actual was X)
- A command output that mentioned an action I should take that the script didn't take itself
- Anything that made me feel "this is a tech demo, not a product"

- [ ] **Step 4: When the script finishes (or aborts), record outcome**

Append to `$NOTES`:

```
## Outcome

End time: HH:MM:SS
Total wall time: NNm NNs
Final state: succeeded | failed at step N
First run status: succeeded | failed | partial_failure | never fired

## Browser interactions required

(List each: GitHub App authorize, Copilot device-flow code, PR merge, …)
```

- [ ] **Step 5: Tear down**

```bash
bash scripts/quickstart-down.sh "${ENV_SUFFIX}"
```

Verify:
- Resource group deletion has started (or completed): `az group exists --name rg-cronfoundry-${ENV_SUFFIX}`
- GitHub App installation revoked: visit https://github.com/settings/installations and confirm
- State file gone: `ls ~/.cronfoundry-quickstart-state-${ENV_SUFFIX} 2>/dev/null` returns "no such file"

- [ ] **Step 6: Commit the round-1 punch list**

```bash
git add "$NOTES"
git commit -m "docs(dogfood): round 1 findings ($(date +%Y-%m-%d))"
```

This is on a worktree branch — do not push to main yet. The punch list will be referenced from PRs that fix individual findings.

---

## Fix the punch list

For each finding in round 1's `$NOTES`:

- [ ] **Per-finding step:** Open a focused branch from `worktree-prerelease-polish-spec`. Implement the fix. Add a test if the fix is in Go code. If the fix is in a shell script, add a bats test if reasonable; otherwise rely on round 2 verification.

Each fix → one PR → one merge. Don't batch fixes — small PRs are easier to review and bisect.

The order of fixes doesn't matter much. Rule: **fix everything marked `blocker` or `friction` before round 2; defer `polish` to a backlog if there are many.**

---

## Round 2 — Verification run

- [ ] **Step 1: Pick a new env suffix**

KV soft-delete prevents reuse of `round1` for 7 days; pick `round2`.

```bash
export ENV_SUFFIX="round2"
```

- [ ] **Step 2: Re-run from the polished branch**

Repeat Steps 1–5 of Round 1 against `docs/superpowers/specs/$(date +%Y-%m-%d)-quickstart-dogfood-round2.md`.

**Goal:** zero entries in the new punch list. Time the run.

- [ ] **Step 3: Compare against round 1**

Append a "Comparison" section to round 2's notes:

```
## Comparison vs round 1

- Round 1 wall time: NNm NNs
- Round 2 wall time: NNm NNs
- Round 1 findings: N (blocker), M (friction), P (polish)
- Round 2 findings: N (blocker), M (friction), P (polish)
- Issues fixed: list
- New issues found: list
```

- [ ] **Step 4: If round 2 has any blocker or friction findings, fix them, then run round 3.**

- [ ] **Step 5: If round 2 is clean, do one more confirmation run with a third env suffix to confirm the script is actually deterministic, then declare Phase 2 done.**

---

## Round 3 (if needed) — Final pass

Same procedure. Stop here regardless of state. Open issues in the repo for any remaining findings, label `polish-deferred`.

---

## Exit criteria

Phase 2 is done when:

- [ ] At least one full run completed end-to-end with zero unplanned interventions.
- [ ] Wall time was <15 minutes.
- [ ] No doc was consulted during the run.
- [ ] Browser interactions were limited to: GitHub App manifest authorize (1 click), Copilot device-flow code entry (1 paste), PR merge in the skill repo (1 click).

If exit criteria are met after round 2, skip round 3.

---

## Test matrix

Mandatory:
- [ ] **macOS** — at least one round.
- [ ] **Linux** — at least one round.

Best-effort (no live dogfood, but visual review of the script's branches):
- [ ] **WSL2** — review the `--platform-check` output and confirm hints are correct. If a Windows operator is available, ask them to do one round.

---

## Cost guardrail

- Each round: ~$5 in Azure resources (Postgres B1ms, Container Apps consumption, Key Vault standard, Log Analytics small).
- Budget for Phase 2: $30 (covers 3 rounds × 2 platforms).
- Tear down between rounds; don't leave RGs hot.

---

## Handoff to Phases 3, 4, 5

Once Phase 2 exits with criteria met, Phases 3 (UI), 4 (docs), and 5 (operator features) can run in parallel. The dogfood punch lists feed:

- **Phase 4** — Round 1's findings seed the troubleshooting guide.
- **Phase 1** — any deferred polish items become small follow-up commits.
