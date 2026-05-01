# Resume Prompt — CronFoundry Phase 1 Implementation (Tasks 4–12)

> Drop this whole file in as the first message of a new session. It is self-contained.

## Context

You are picking up an in-progress execution of **Phase 1 of the CronFoundry pre-release polish pass**. The plan is fully written; tasks 1–3 are merged on a draft PR. You are continuing tasks 4–12 using the **superpowers:subagent-driven-development** skill (fresh subagent per task, two-stage review: spec compliance, then code quality).

### Repository

- Path: `/home/tng/workspace/cronfoundry`
- The implementation lives in a worktree: `.claude/worktrees/phase1-installer` on branch `worktree-phase1-installer`.
- The plans/spec live in another worktree: `.claude/worktrees/prerelease-polish-spec` on branch `worktree-prerelease-polish-spec` (open as PR #46). The plan file you'll be working from is **only present in that worktree**, not on `main` and not on the phase1 branch.

### Open PRs

- **PR #46** — design spec + 5 plans. `worktree-prerelease-polish-spec`. Awaiting review.
- **PR #47 (draft)** — Phase 1 in progress, tasks 1–3 of 12 complete. `worktree-phase1-installer`. You are continuing this PR.

### Project memory rules (do not violate)

- **Never push to main.** Always use the open PR branch.
- **Always work in a git worktree** for any edit (including docs/specs).
- **Secrets are in `.env`** at the repo root.

### Auto Mode

Operator has auto-mode active: execute autonomously, minimize interruptions, prefer action over planning. Continue dispatching subagents without pausing for confirmation between tasks unless something blocks you.

---

## What's already done (do not redo)

Branch `worktree-phase1-installer` has these 4 commits ahead of main:

| SHA | Subject |
|-----|---------|
| `49b8e91` | refactor(quickstart): extract state helpers into scripts/lib/state.sh |
| `1e16070` | fix(quickstart): atomic state_save and literal key matching |
| `87b7369` | feat(quickstart): add step framework with idempotent verifiers |
| `cc74155` | feat(quickstart): require gh CLI in prereq check |

Files added/modified so far:
- `scripts/lib/state.sh` (new) — `state_path_for`, `state_init`, `state_load`, `state_save`, `state_clear`. Atomic single-`mv` upsert, literal key matching via `awk`, exports key for in-process reads.
- `scripts/lib/state_test.bats` + `scripts/lib/state_test.sh` (fallback runner — `bats` is not installed in the sandbox; both files are committed and the `.sh` is what actually verifies). 6 tests, all passing.
- `scripts/lib/steps.sh` (new) — `step_run NAME VERIFIER BODY` framework. Verifier-first idempotency.
- `scripts/lib/steps_test.bats` + `scripts/lib/steps_test.sh`. 4 tests, all passing.
- `scripts/quickstart-copilot.sh` — sources both libs near the top; `save()` legacy alias added; `gh` added to prereq check after `check_cmd go`.

**The script's existing `header "[step N/17] …"` calls have NOT been rewritten to use `step_run` yet.** That happens incrementally in Tasks 4–11.

---

## Your mission

Implement Tasks 4–12 of the Phase 1 plan via subagent-driven-development. Hand off at the end with a final code review across the whole branch and prepare PR #47 to come out of draft.

### Task list to track

Create todos for all 9 remaining tasks at the start. Mark them in_progress / completed as you go.

| # | Task | Risk | Notes |
|---|---|---|---|
| 4 | Reorder — Bicep deploy before GitHub App registration | **High** | Largest single change. Requires confirming serve binary tolerates empty `CRONFOUNDRY_GITHUB_*` env vars (PR #45 implies it does — verify via `internal/bootstrap` and `cmd/cronfoundry/serve.go`). Bicep params for App fields must allow empty defaults. |
| 5 | Use manifest flow for App registration | Medium | `cronfoundry setup github-app` exists (PR #45). May need to add `--homepage-url`, `--callback-url`, `--webhook-url` flags + corresponding fields on `internal/githubapp.ManifestInput` if missing. TDD that flags reach rendered manifest JSON. |
| 6 | Push App credentials to Container App after registration | Low | `az containerapp update --set-env-vars` for the 5 App env vars. Confirm var names match what `serve` reads (likely `CRONFOUNDRY_GITHUB_APP_ID`, `CRONFOUNDRY_GITHUB_CLIENT_ID`, `CRONFOUNDRY_GITHUB_CLIENT_SECRET`, `CRONFOUNDRY_GITHUB_APP_PEM` (raw, not B64 — verify), `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`). Wait for revision health via `/healthz` (verify endpoint exists). |
| 7 | Auto-discover installation ID + mint-jwt CLI | Medium | New `cmd/cronfoundry/setup_mintjwt.go` Go command with TDD. Extract `MintAppJWT` if not already in `internal/githubapp` (`grep -rn "RS256" internal/`). Then bash poll loop over `/app/installations`. |
| 8 | Add `cronfoundry admin connect-copilot` CLI | Medium-High | New Go cmd at `cmd/cronfoundry/admin_connectcopilot.go`. TDD with stubbed device-flow endpoints (test code in plan). Wire real `StoreToken` via existing `internal/llm/copilot` package — verify how UI device-flow stores today (`grep -rn "device.code\|verification_uri\|user_code" internal/llm/ internal/webapi/`). |
| 9 | Wire connect-repo, set-secret, connect-copilot into install.sh | Low | Three CLI calls replacing the §17 manual UI clicks. Each admin command may need `--base-url` flag (verify with `--help`). |
| 10 | Auto-push starter skill via gh api | Low | New templates under `scripts/templates/`. Branch + 2 file commits + open PR via `gh`. Skip cleanly if `cronfoundry.yaml` exists. |
| 11 | Wait for first green run after install | Low | Poll `/api/runs?limit=1`. May hit auth — fall back to printing dashboard URL if so. |
| 12 | `quickstart-down.sh` teardown script | Low | `az group delete` + revoke install via App JWT + `state_clear`. Per-env. |

### Process

For each task in sequence:

1. **Read the task text** from the plan. The plan file is at `/home/tng/workspace/cronfoundry/.claude/worktrees/prerelease-polish-spec/docs/superpowers/plans/2026-04-30-prerelease-phase1-installer.md`. Use absolute path with `Read` — you are working in the phase1 worktree but the plan is in the spec worktree.
2. **Dispatch an implementer subagent** with the full task text inline (don't make the subagent read the plan file). Include:
   - The relevant prior-task context (what files already exist, what conventions to follow).
   - The bats fallback note: bats is not installed; mirror `scripts/lib/state_test.sh`'s subshell+`set -e`+`[[ ]]` pattern for any new bash tests.
   - For Go work, mention that `make test-short` runs unit tests fast; `make test` requires Docker; `make sqlc` if queries change.
3. **If implementer asks questions**, answer them.
4. **Spec compliance review** — dispatch `general-purpose` subagent that reads the actual files and verifies against the spec.
5. **If issues, send implementer back to fix**, then re-review.
6. **Code quality review** — dispatch `pr-review-toolkit:code-reviewer` subagent against the commit SHA(s). Strengths first, then issues by severity.
7. **If issues, fix loop**.
8. **Mark task complete** in TodoWrite.
9. **After every 2–3 tasks**, push to the branch so the draft PR #47 stays current.

### After all 12 tasks

1. Final whole-branch code review via the `pr-review-toolkit:code-reviewer` subagent against `git log main..HEAD`.
2. Update PR #47's body with the full task list checked off.
3. Mark PR #47 ready-for-review (out of draft) only if final review approved AND tests pass: `make test-short`, `bash scripts/lib/state_test.sh`, `bash scripts/lib/steps_test.sh`, `bash -n scripts/quickstart-copilot.sh`, `bash -n scripts/quickstart-down.sh`.
4. Use the `superpowers:finishing-a-development-branch` skill to wrap up.

---

## Important environmental notes

- **bats is not installed** in this sandbox. Every shell test must also have a `*_test.sh` fallback runner using `(set -e; ...)` subshells with `[[ ... ]]` final assertions and an explicit `exit 1` on failure aggregation. See `scripts/lib/state_test.sh` as the canonical pattern.
- **Some Bash commands are blocked by the auto-mode sandbox.** Plain checks like `which bats`, `find . -name install.sh` may be denied. Workarounds: use `Read` for files, `Grep` (`Bash` with `grep -rn ... 2>/dev/null | head`) for searches, prefix probes with `2>&1 || echo X` so they always succeed.
- **Do not push to main.** Always push to `worktree-phase1-installer`.
- **The user has been notified** of the pause point. They are NOT actively watching this session at start; they pasted this prompt to resume autonomous execution.

---

## Quick orientation commands (run these at session start)

```bash
cd /home/tng/workspace/cronfoundry/.claude/worktrees/phase1-installer
git status
git log --oneline main..HEAD
gh pr view 47 --json state,isDraft,title
```

Then read the Phase 1 plan once for the full Task 4 spec text:

```
Read /home/tng/workspace/cronfoundry/.claude/worktrees/prerelease-polish-spec/docs/superpowers/plans/2026-04-30-prerelease-phase1-installer.md (offset ~363, limit ~150 for Task 4)
```

Then start dispatching subagents.

---

## Brief reminder: the design philosophy

`install.sh` is the product surface, not the doc. Every operator-facing prompt or paste-back is friction we need to eliminate. The end-state is: operator runs `bash <(curl …/install.sh)`, answers ~3 unavoidable prompts (Azure subscription, region, env suffix) plus the OAuth device-flow user-code, and ends with a green Copilot Enterprise run firing every 5 minutes within 15 minutes wall time. No copy-paste of IDs, no leaving the terminal except for the unavoidable browser interactions.

Tasks 4–8 are where this actually happens (Bicep-first reorder enables real-FQDN App registration → no post-deploy URL patch; manifest flow eliminates the 4-value paste; auto-discover eliminates the install-ID paste; new admin CLIs eliminate the 4 in-UI clicks; auto-push eliminates the manual yaml authoring). Tasks 9–12 are the glue.

Good luck. Proceed.
