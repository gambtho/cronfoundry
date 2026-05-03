-- name: CountQueueDepth :one
SELECT count(*)::bigint
FROM run
WHERE org_id = $1 AND status IN ('pending','running');

-- name: CountActiveWorkers :one
SELECT count(DISTINCT runner_pid)::bigint
FROM run
WHERE org_id = $1 AND status = 'running' AND runner_pid IS NOT NULL;

-- name: LastRunCreatedAt :one
SELECT MAX(created_at)::timestamptz AS last
FROM run
WHERE org_id = $1;
