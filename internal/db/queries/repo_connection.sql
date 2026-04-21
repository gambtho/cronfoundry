-- name: InsertRepoConnection :one
INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch, sync_interval_sec)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (org_id, owner, name) DO UPDATE
  SET github_app_install_id = EXCLUDED.github_app_install_id,
      default_branch        = EXCLUDED.default_branch,
      sync_interval_sec     = EXCLUDED.sync_interval_sec
RETURNING *;

-- name: GetRepoConnection :one
SELECT *
FROM repo_connection
WHERE id = $1;

-- name: GetRepoConnectionByOwnerName :one
SELECT *
FROM repo_connection
WHERE org_id = $1 AND owner = $2 AND name = $3;

-- name: ListRepoConnections :many
SELECT *
FROM repo_connection
WHERE org_id = $1
ORDER BY owner, name;

-- name: ListDueRepoConnections :many
-- Returns repos whose last sync is older than sync_interval_sec, or which
-- have never been synced.
SELECT *
FROM repo_connection
WHERE last_synced_at IS NULL
   OR last_synced_at + make_interval(secs => sync_interval_sec) <= now()
ORDER BY coalesce(last_synced_at, to_timestamp(0));

-- name: DeleteRepoConnection :execrows
DELETE FROM repo_connection
WHERE id = $1
  AND org_id = $2;

-- name: MarkRepoSyncedOK :exec
UPDATE repo_connection
SET last_synced_at       = now(),
    last_synced_head_sha = $2,
    last_sync_error      = NULL
WHERE id = $1;

-- name: MarkRepoSyncError :exec
UPDATE repo_connection
SET last_synced_at  = now(),
    last_sync_error = $2
WHERE id = $1;
