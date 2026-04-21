# P7 Post-Merge Checklist

Steps to close out MVP once P6 and P7 are both merged.

**Owner legend:** 🧑 = user action, 🤖 = Claude action.

---

## 1. Land P6

- [ ] 🧑 Review and merge **PR #13** (`feature/p6-mvp-gaps` → `main`).
- [ ] 🧑 Ping Claude once merged.

## 2. Rebase and open P7

- [ ] 🤖 `git fetch origin`
- [ ] 🤖 From `.worktrees/p7-mvp-closeout`: `git rebase origin/main`.
- [ ] 🤖 Resolve any conflicts. Most likely hotspot: `README.md` (P6 adds a "Recent additions" section; P7 rewrites Status/Roadmap/Architecture). Manual merge keeps both — P7 sections land as written, P6 section stays.
- [ ] 🤖 `cd web && npm test` → 8 pass; `npx tsc --noEmit` → clean; `npm run build` → clean.
- [ ] 🤖 `go build ./...` and `go test -short ./...` on the rebased tree → clean.
- [ ] 🤖 Push: `git push -u origin feature/p7-mvp-closeout`.
- [ ] 🤖 Open PR with:
  - Title: `feat(p7): MVP close-out — LogTail + docs refresh + Azure smoke runbook`
  - Body: lead with LogTail.tsx; link spec + plan; flag the two pending manual tasks (browser verification, live Azure smoke).

## 3. Review and merge P7

- [ ] 🧑 Review the PR (expect it to be small: ~115 LoC new component + 1 test file + docs).
- [ ] 🧑 Address CodeRabbit feedback if any (use `my:fix-pr` if needed).
- [ ] 🧑 Merge when green.

## 4. Browser verification of LogTail (Plan Task 10)

Goal: confirm `LogTail` behaves correctly in a real browser before running the full Azure smoke.

- [ ] 🧑 Pull main locally: `git checkout main && git pull`.
- [ ] 🧑 Start the local dev harness: `make dev` (Docker Compose: Postgres + cronfoundry).
- [ ] 🧑 `make migrate` if it's a fresh DB.
- [ ] 🧑 Start Vite: `cd web && npm run dev`. Opens on `http://localhost:5173`, proxies `/api` to `:8080`.
- [ ] 🧑 Log in via GitHub OAuth. Connect a skill repo via the admin CLI if there isn't one already:
  ```bash
  ./cronfoundry admin connect-repo <owner>/<repo> --installation-id <id>
  echo -n 'sk-...' | ./cronfoundry admin set-secret openai_key
  ```
- [ ] 🧑 Trigger a run (dashboard → **Run now**, or wait for a natural fire).
- [ ] 🧑 Open the run detail; verify:
  - [ ] Log panel shows rows streaming in (~2s cadence).
  - [ ] Auto-scrolls to bottom on each new row.
  - [ ] Scrolling up pauses auto-scroll; scrolling back to the bottom resumes it.
  - [ ] When run finishes, stream closes (no console errors; Network tab shows SSE connection ends with `event: done`).
  - [ ] Re-opening the drawer on a finished run shows the same events via static fetch (no EventSource in Network tab).
- [ ] 🧑 If anything misbehaves → open an issue, cut a fix branch, repeat.

## 5. Live Azure smoke (Plan Task 15)

Walk `docs/guides/smoke-test-mvp-azure.md` top-to-bottom against a fresh Azure subscription.

- [ ] 🧑 Provision an Azure subscription / resource group.
- [ ] 🧑 Register the GitHub App per section 2.
- [ ] 🧑 Bicep deploy per section 3.
- [ ] 🧑 First-boot config per section 4.
- [ ] 🧑 Land a skill per section 5.
- [ ] 🧑 Observe the first fire per section 6 (verify `LogTail` streams in Azure too).
- [ ] 🧑 Verify the three side effects per section 7:
  - [ ] Slack message.
  - [ ] GitHub issue filed.
  - [ ] `memory.md` commit from `cronfoundry[bot]`.
- [ ] 🧑 Verify the audit log per section 8 (P6c Audit page).
- [ ] 🧑 Teardown per section 9.

**If a step fails:**

- Identify whether the fix belongs in code or docs.
- For doc fixes → edit `smoke-test-mvp-azure.md` and amend the committed runbook (`git commit --amend` is fine if still on branch, else a follow-up commit to main).
- For code fixes → open a focused follow-up PR, merge, re-run from the failed step.
- Never mark the pass/fail checklist clean until a full run passes end-to-end.

## 6. Close-out

- [ ] 🧑 Update the project status in `README.md` / `docs/index.html` if the smoke run surfaced any stale claim.
- [ ] 🤖 Save a feedback memory if the smoke run revealed any non-obvious lesson worth remembering for future projects (e.g., "Container Apps Jobs dispatch takes ~30s cold start, factor into timeout_sec defaults").
- [ ] 🧑 Announce MVP shipped. 🎉

## Rollback plan (unlikely)

If the live smoke surfaces a P-0 regression that can't be fixed in a short follow-up:

- [ ] Revert the offending PR(s) on `main`.
- [ ] Re-deploy the previous Container App image tag via:
  ```bash
  az containerapp update --resource-group rg-cronfoundry-<env> \
    --name api --image ghcr.io/cronfoundry/api:<prev-tag>
  ```
- [ ] Re-open the PR(s) on a fix branch and iterate until green.

No rollback plan for DB migrations is included: both P6 and P7 are additive (new table `app_user`, new queries, new endpoint). Rolling the image back is safe; rolling the schema is a separate exercise if ever needed.
