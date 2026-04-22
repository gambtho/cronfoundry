-- name: InsertRun :one
-- Idempotent for scheduled fires (ON CONFLICT on the partial unique index
-- run_schedule_firetime_unique). Manual runs always have fire_time=NULL and
-- pass through without collision — the index predicate (WHERE fire_time IS
-- NOT NULL) must be repeated on ON CONFLICT so Postgres matches the partial
-- index rather than requiring a full unique constraint.
--
-- On conflict (i.e. another concurrent tick already inserted the same
-- (schedule_id, fire_time) row), this query returns the existing row with
-- `inserted=false`. This lets callers distinguish a brand-new insert
-- (`inserted=true`) from a concurrent-duplicate outcome — without falling
-- back to a zero-UUID sentinel that's easy to misuse.
WITH ins AS (
    INSERT INTO run (
        org_id, schedule_id, skill_sha, fire_time, status, fire_reason, actor,
        runner_token_hash
    )
    VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7)
    ON CONFLICT (schedule_id, fire_time) WHERE fire_time IS NOT NULL DO NOTHING
    RETURNING *, true AS inserted
)
SELECT * FROM ins
UNION ALL
SELECT *, false AS inserted FROM run
  WHERE schedule_id = $2 AND fire_time = $4
  AND NOT EXISTS (SELECT 1 FROM ins)
LIMIT 1;

-- name: GetRun :one
SELECT *
FROM run
WHERE id = $1;

-- name: GetRunForContext :one
-- Returns the run + its schedule + skill + repo so the runner can assemble
-- its full context in one query.
SELECT r.*,
       s.name  AS schedule_name,
       s.cron,
       s.timezone,
       s.timeout_sec,
       s.provider,
       s.model,
       s.llm_secret_ref,
       s.llm_endpoint,
       s.llm_deployment,
       s.destinations_json,
       s.writeback_json,
       s.env_json,
       sk.id   AS skill_id_joined,
       sk.path AS skill_path,
       sk.repo_id AS skill_repo_id,
       sk.frontmatter_json,
       rc.owner,
       rc.name               AS repo_name,
       rc.default_branch,
       rc.github_app_install_id
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;

-- name: SetRunRunning :one
UPDATE run
SET status     = 'running',
    started_at = now(),
    runner_pid = $2
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: FinalizeRun :one
-- Guarded against stale/duplicate finalization: the WHERE clause refuses to
-- update a row already in a terminal state (succeeded / partial_failure /
-- failed). A runner that crashed and restarted can't clobber the original
-- finalization — the query returns zero rows (pgx.ErrNoRows) and the
-- handler maps that to 409 Conflict.
UPDATE run
SET status               = $2,
    finished_at          = now(),
    duration_ms          = $3,
    tokens_in            = $4,
    tokens_out            = $5,
    cost_cents           = $6,
    error_kind           = $7,
    error_msg            = $8,
    writeback_commit_sha = $9
WHERE id = $1
  AND status NOT IN ('succeeded', 'partial_failure', 'failed')
RETURNING *;

-- name: ListActiveRunsForSchedule :many
-- Used for overlap-policy decisions. Returns runs in non-terminal states.
SELECT *
FROM run
WHERE schedule_id = $1
  AND status IN ('pending', 'running')
ORDER BY created_at ASC;

-- name: DeleteRun :exec
-- Used when `skip` overlap policy discards the freshly-inserted pending row.
DELETE FROM run WHERE id = $1;

-- name: OrphanSweep :execrows
-- Marks non-terminal runs as failed if they've been sitting longer than
-- their schedule's timeout + 5-minute grace. Recovers from runner crashes
-- and service restarts.
UPDATE run
SET status      = 'failed',
    error_kind  = 'shutdown',
    error_msg   = COALESCE(error_msg, 'orphan sweep: run exceeded timeout'),
    finished_at = now()
FROM schedule s
WHERE run.schedule_id = s.id
  AND run.status IN ('pending', 'running')
  AND now() - COALESCE(run.started_at, run.created_at) > (s.timeout_sec + 300) * interval '1 second';

-- name: ListRunsForOrg :many
-- Used by `cronfoundry admin list-runs`. Returns the most recent N runs,
-- joined to schedule + skill names for display.
SELECT r.id,
       r.status,
       r.fire_reason,
       r.actor,
       r.started_at,
       r.finished_at,
       r.duration_ms,
       r.error_kind,
       r.error_msg,
       r.created_at,
       s.name       AS schedule_name,
       sk.path      AS skill_path,
       rc.owner,
       rc.name      AS repo_name
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.org_id = $1
ORDER BY r.created_at DESC
LIMIT $2;

-- name: ListRunsForSchedule :many
-- Same shape as ListRunsForOrg but filtered to a single schedule by name.
SELECT r.id,
       r.status,
       r.fire_reason,
       r.actor,
       r.started_at,
       r.finished_at,
       r.duration_ms,
       r.error_kind,
       r.error_msg,
       r.created_at,
       s.name       AS schedule_name,
       sk.path      AS skill_path,
       rc.owner,
       rc.name      AS repo_name
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.org_id = $1
  AND s.name = $2
ORDER BY r.created_at DESC
LIMIT $3;

-- name: GetRunWritebackConfig :one
SELECT s.writeback_json,
       rc.github_app_install_id,
       rc.owner,
       rc.name AS repo_name
FROM run r
JOIN schedule s ON s.id = r.schedule_id
JOIN skill sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;

-- name: GetRunForAdmin :one
-- Used by `cronfoundry admin show-run`.
SELECT r.id,
       r.org_id,
       r.schedule_id,
       r.skill_sha,
       r.fire_time,
       r.status,
       r.fire_reason,
       r.actor,
       r.started_at,
       r.finished_at,
       r.duration_ms,
       r.tokens_in,
       r.tokens_out,
       r.cost_cents,
       r.error_kind,
       r.error_msg,
       r.writeback_commit_sha,
       r.created_at,
       s.name  AS schedule_name,
       s.cron,
       sk.path AS skill_path,
       rc.owner,
       rc.name AS repo_name
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;

-- name: ListRecentTerminalScheduledRuns :many
-- Used by evaluateAutoPause. Returns the last N terminal scheduled runs for
-- a schedule, within the anti-flap window defined by last_enabled_at.
-- Uses `created_at` (NOT NULL, matches the existing run_schedule_created_idx)
-- so failed-before-dispatch runs (started_at NULL) are still counted.
SELECT status
FROM run
WHERE schedule_id = $1
  AND fire_reason = 'schedule'
  AND status IN ('succeeded', 'partial_failure', 'failed')
  AND created_at >= $2
ORDER BY created_at DESC
LIMIT $3;
