-- name: InsertRun :one
-- Idempotent for scheduled fires (ON CONFLICT on the partial unique index
-- run_schedule_firetime_unique). Manual runs always have fire_time=NULL and
-- pass through without collision.
INSERT INTO run (
    org_id, schedule_id, skill_sha, fire_time, status, fire_reason, actor,
    runner_token_hash
)
VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7)
ON CONFLICT (schedule_id, fire_time) DO NOTHING
RETURNING *;

-- name: GetRun :one
SELECT *
FROM run
WHERE id = $1;

-- name: GetRunForContext :one
-- Returns the run + its schedule + skill + repo so the runner can assemble
-- its full context in one query.
SELECT r.*,
       s.cron,
       s.timezone,
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
