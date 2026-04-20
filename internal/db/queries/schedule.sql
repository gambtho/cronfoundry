-- name: UpsertSchedule :one
INSERT INTO schedule (
    org_id, skill_id, name, cron, timezone, overlap_policy, timeout_sec,
    enabled, provider, model, llm_secret_ref, llm_endpoint, llm_deployment,
    destinations_json, writeback_json, env_json, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now())
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
SELECT s.*, sk.path AS skill_path, sk.name AS skill_name, rc.owner, rc.name AS repo_name
FROM schedule s
JOIN skill sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE s.org_id = $1
ORDER BY rc.owner, rc.name, sk.path, s.name;

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
