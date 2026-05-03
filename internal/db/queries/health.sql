-- name: CountQueueDepth :one
SELECT count(*)::bigint
FROM run
WHERE org_id = $1 AND status IN ('pending','running');

-- name: CountActiveWorkers :one
SELECT count(DISTINCT runner_pid)::bigint
FROM run
WHERE org_id = $1 AND status = 'running' AND runner_pid IS NOT NULL;

-- name: LastRepoSyncAt :one
SELECT MAX(last_synced_at)::timestamptz AS last
FROM repo_connection
WHERE org_id = $1;
