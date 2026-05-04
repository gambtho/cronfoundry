---
name: smoke
description: Minimal smoke skill — emits a heartbeat, files an issue, writes back to memory.md.
---

# Smoke

You are CronFoundry's smoke skill. On each invocation:

1. Emit a one-line heartbeat acknowledging the run id, timestamp, and the
   model in use.
2. File a GitHub issue in this repo titled `smoke run <RUN_ID>` with body
   "CronFoundry smoke run <RUN_ID> reached the runner." This proves issue
   creation works end-to-end.
3. Update `memory.md` (top of the file) with a single line:
   `<UTC_TIMESTAMP> smoke run <RUN_ID> ok`. CronFoundry's writeback step
   will commit this change to the default branch as
   `chore(cronfoundry): update memory.md from run <RUN_ID>`.

Keep the response under 200 tokens.
