-- name: InsertRunNotification :exec
INSERT INTO run_notification (run_id, org_id, kind, target, status, reason)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListRunNotifications :many
SELECT id, run_id, kind, target, status, reason, created_at
FROM run_notification
WHERE run_id = $1 AND org_id = $2
ORDER BY id ASC;
