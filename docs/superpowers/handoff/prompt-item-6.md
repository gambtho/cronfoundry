# Item #6: Simplify the Azure deployment runbook

## Background

`docs/guides/smoke-test-mvp-azure.md` is the runbook that tells a fresh
operator how to deploy CronFoundry to Azure from an empty subscription.
`docs/guides/smoke-test-mvp-azure-findings.md` (724 lines, 24 numbered
findings) is a running log of every cliff that was hit while one of the
maintainers tried to follow it themselves. Many findings (param names,
missing required params, ingress default, master-key generation, image
publishing) are exactly the cliffs a first user would fall off.

PRD goal #1 says "A Platform Engineer can stand up their first working
scheduled skill in one afternoon." That goal cannot be claimed met until a
fresh operator with no help can do it. The release-readiness review's
finding was not "fix one bug" but "**dramatically simplify**" — the runbook
needs major reduction in steps + automation of the known cliffs.

## Goal

A new self-hoster, with no project knowledge, can go from empty Azure
subscription to a green run in under 2 hours of clock time, following the
runbook with no help.

## How to start

1. Open this worktree:
   ```bash
   cd /home/tng/workspace/cronfoundry
   git worktree add .claude/worktrees/spec-runbook -b worktree-spec-runbook main
   cd .claude/worktrees/spec-runbook
   ```
2. Read `00-context.md` for project conventions.
3. Read the current runbook + findings:
   - `docs/guides/smoke-test-mvp-azure.md` (the runbook itself)
   - `docs/guides/smoke-test-mvp-azure-findings.md` (24 findings, F1-F24
     plus session 2-4 follow-ups)
   - `docs/guides/deploy-azure.md` (lighter operator doc, may want to merge)
   - `deploy/main.bicep` and `deploy/params.example.json`
4. Brainstorm what to cut and what to automate.

## Brainstorm questions

This is the most open-ended of the remaining items. Things to settle with
the user:

1. **One runbook or two?** Today: `smoke-test-mvp-azure.md` (long, exhaustive)
   + `deploy-azure.md` (short, normative). Possibilities:
   - Merge into one canonical "deploy" doc.
   - Keep two: `quickstart.md` (under 30 minutes, happy path only) and
     `deploy-azure.md` (full reference). Move smoke-test to a CI thing not
     a doc.
2. **What to automate?** Each of the 24 findings is an automation candidate.
   Categories:
   - **Bicep param mistakes (F1, F2, F3)** → fix `params.example.json` so
     it works as-is for a copy-paste flow; document only the values the
     operator actually changes.
   - **Master key generation (F4)** → make `cronfoundry admin init` print
     copy-paste Bicep param JSON.
   - **Image publish (F5)** → trigger a release on first deploy if the tag
     doesn't exist yet, or document the dependency clearly.
   - **GHCR visibility, KV secrets, FQDN discovery (F6, F7+)** → automation,
     defaults that fail closed, or a "verify" CLI.
   - **Post-deploy verification** → a `cronfoundry diagnose` subcommand?
3. **Worth a `cronfoundry-bootstrap` CLI?** PRD risks section
   mentions this as a fast-follow. Could collapse the runbook to "run this
   one tool, follow its prompts." Heavier lift. Probably out of scope for
   this item but worth flagging.
4. **Do the smoke-test findings need to remain a doc?** They're useful
   internal history but live in `docs/`. Move to `.smoke-history/` or
   commit history?

## What to deliver

Standard flow:

1. Spec → `docs/superpowers/specs/2026-04-29-runbook-simplification-design.md`
   with: target structure (1 doc vs 2), what's cut, what's automated, what
   moves to a CLI subcommand if any, target time-to-first-run.
2. Plan → `docs/superpowers/plans/2026-04-29-runbook-simplification.md` with
   per-step tasks. Probably mixes doc edits + small Go work (master-key
   helper, diagnose command, or bicep defaults).
3. PR with title `docs: simplify Azure deploy runbook` or similar.

The PR body should include the **before/after step count** and the
**before/after estimated time** with rationale.

## Acceptance

1. The canonical "how to deploy" doc has under ~10 numbered steps.
2. Every required Bicep parameter has either a working default in
   `params.example.json` or an explicit instruction with a runnable command.
3. None of the F1-F24 cliffs survive: re-reading each, the runbook now
   handles or pre-empts each one.
4. (Stretch) A second person — preferably someone outside the project —
   walks through the runbook from scratch and succeeds without asking
   questions. Document the result in the PR.
5. The findings file is either retired (moved out of `docs/`) or clearly
   labeled as historical.

## Test signal

Try it yourself in a clean Azure subscription before opening the PR. If you
can deploy successfully reading only the canonical doc, that's the bar.

## Out of scope

- Multi-cloud deploy (AKS, Fly already have separate docs).
- The `cronfoundry-bootstrap` CLI as a full project — flag as fast-follow if
  appropriate.
- Documentation styling / branding refresh.
