-- name: InsertAuditLog :exec
INSERT INTO audit_log (org_id, actor, action, target_kind, target_id, detail_json)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAuditLogByOrg :many
SELECT *
FROM audit_log
WHERE org_id = $1
ORDER BY ts DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: CountAuditLogByOrg :one
SELECT count(*) AS total
FROM audit_log
WHERE org_id = $1;
