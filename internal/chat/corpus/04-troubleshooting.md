# Troubleshooting common failures

When a run shows status `failed` or `partial_failure`, look at:

1. The run's `error_kind` and `error_msg`. Common kinds:
   - `manifest_parse` — malformed cronfoundry.yaml; the operator must fix
     and recommit.
   - `skill_load` — SKILL.md missing, frontmatter invalid, or include
     path escapes the skill directory.
   - `llm_error` — provider returned an error. Check API key, model name,
     quota, and the upstream provider's status.
   - `timeout` — exceeded `timeout_sec`. Bump it or shorten the prompt.
   - `secret_missing` — manifest references a secret name that's not in
     the secret store. Add it via Settings → Secrets.
   - `mcp_server_start_failed` / `mcp_env_resolve` — tool-using skill
     misconfigured an MCP server.
   - `writeback_*` — the writeback commit/push failed (auth, conflict).
2. The run's event timeline (`get_run_events`). Events flow:
   `run.started` → `manifest.set` → `secret.fetched` (per secret) →
   `llm.start` → `llm.end` → `publish.*` (per destination) → optional
   `writeback.*` → `run.finished`.
3. The `partial_failure` status means the LLM call succeeded but at least
   one destination failed to publish (e.g. broken Slack webhook). Other
   destinations still published. Check `get_run_notifications` for the
   per-destination status.
4. Auto-pause kicks in after N consecutive failures (per-schedule
   `auto_pause_after`). A paused schedule will not fire until resumed
   from the UI.

Quiet jobs: if a schedule hasn't run successfully in the expected window,
the Overview page surfaces it as a "quiet job" alert. Most common causes:
the cron is malformed, the schedule is paused, or the underlying skill
was deleted from the repo.
