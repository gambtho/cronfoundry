-- name: UpsertSchedule :one
INSERT INTO schedule (
    org_id, skill_id, name, cron, timezone, overlap_policy, timeout_sec,
    enabled, provider, model, llm_secret_ref, llm_endpoint, llm_deployment,
    destinations_json, writeback_json, env_json, auto_pause_after, mcp_env_json, max_turns, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, now())
ON CONFLICT (skill_id, name) DO UPDATE
  SET cron              = EXCLUDED.cron,
      timezone          = EXCLUDED.timezone,
      overlap_policy    = EXCLUDED.overlap_policy,
      timeout_sec       = EXCLUDED.timeout_sec,
      enabled           = EXCLUDED.enabled,
      provider          = EXCLUDED.provider,
      model             = EXCLUDED.model,
      llm_secret_ref    = EXCLUDED.llm_secret_ref,
      llm_endpoint      = EXCLUDED.llm_endpoint,
      llm_deployment    = EXCLUDED.llm_deployment,
      destinations_json = EXCLUDED.destinations_json,
      writeback_json    = EXCLUDED.writeback_json,
      env_json          = EXCLUDED.env_json,
      auto_pause_after  = EXCLUDED.auto_pause_after,
      mcp_env_json      = EXCLUDED.mcp_env_json,
      max_turns         = EXCLUDED.max_turns,
      updated_at        = now()
RETURNING *;

-- name: DisableMissingSchedules :exec
-- Sets enabled=false on any schedule under `skill_id` whose name is NOT in
-- the given slice. Schedules are soft-disabled (not deleted) to preserve
-- run history.
UPDATE schedule
SET enabled    = false,
    updated_at = now()
WHERE skill_id = $1
  AND enabled = true
  AND NOT (name = ANY($2::text[]));

-- name: ListSchedulesByOrg :many
SELECT s.*, sk.path AS skill_path, sk.name AS skill_name, sk.frontmatter_json AS skill_frontmatter_json, rc.owner, rc.name AS repo_name
FROM schedule s
JOIN skill sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE s.org_id = $1
ORDER BY rc.owner, rc.name, sk.path, s.name;

-- name: SetScheduleEnabled :one
-- On enable: clear any auto-pause state. last_enabled_at only advances on a
-- real false→true transition (not on an idempotent enable-already-enabled
-- call), so the anti-flap window isn't silently reset when, e.g., two UI
-- tabs race to click Resume. On disable: leave the auto-pause columns
-- untouched — a user-initiated pause doesn't touch auto-pause state.
UPDATE schedule
SET enabled = $2,
    auto_paused_at    = CASE WHEN $2 THEN NULL                   ELSE auto_paused_at    END,
    auto_pause_reason = CASE WHEN $2 THEN NULL                   ELSE auto_pause_reason END,
    last_enabled_at   = CASE WHEN $2 AND NOT enabled THEN now()  ELSE last_enabled_at   END,
    updated_at        = now()
WHERE id = $1
  AND org_id = $3
RETURNING *;

-- name: ListDueSchedules :many
-- Returns schedules ready to fire: enabled AND next_fire_at <= now.
-- Ordered by next_fire_at so we dispatch oldest-due first.
SELECT *
FROM schedule
WHERE enabled = true
  AND next_fire_at IS NOT NULL
  AND next_fire_at <= now()
ORDER BY next_fire_at ASC;

-- name: UpdateScheduleNextFireAt :exec
UPDATE schedule
SET next_fire_at = $2,
    updated_at   = now()
WHERE id = $1;

-- name: GetScheduleForTrigger :one
-- Used by the manual trigger endpoint to assemble a new run row.
-- Returns the schedule plus the skill's current_sha so the INSERT can be
-- atomic (rather than two round trips).
SELECT s.id       AS schedule_id,
       s.org_id,
       s.skill_id,
       sk.current_sha AS skill_sha
FROM schedule s
JOIN skill sk ON sk.id = s.skill_id
WHERE s.id = $1;

-- name: ListDueSchedulesWithSha :many
-- Like ListDueSchedules but joins the skill to include current_sha so
-- the scheduler can set it on the new run row without a second query.
SELECT s.*,
       sk.current_sha AS skill_sha
FROM schedule s
JOIN skill sk ON sk.id = s.skill_id
WHERE s.enabled = true
  AND s.next_fire_at IS NOT NULL
  AND s.next_fire_at <= now()
ORDER BY s.next_fire_at ASC;

-- name: GetScheduleAutoPauseConfig :one
-- Returns the fields evaluateAutoPause needs to decide whether to trigger a
-- pause and emit audit/run_event rows: org_id (for audit), the per-schedule
-- threshold override (nullable), and the anti-flap window boundary.
-- `enabled` is returned for tests/debug; the pause query guards on it
-- independently via `WHERE enabled = true`.
SELECT org_id, auto_pause_after, last_enabled_at, enabled
FROM schedule
WHERE id = $1;

-- name: AutoPauseSchedule :execrows
-- Idempotent conditional pause. Returns the number of rows affected so the
-- caller can distinguish "we paused it" (1) from "someone else already paused
-- it" (0, in which case the caller must not emit duplicate audit rows).
UPDATE schedule
SET enabled           = false,
    auto_paused_at    = now(),
    auto_pause_reason = $2,
    updated_at        = now()
WHERE id = $1
  AND enabled = true;
