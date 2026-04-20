-- name: CreateOrganization :one
INSERT INTO organization (name)
VALUES ($1)
RETURNING *;

-- name: GetFirstOrganization :one
SELECT *
FROM organization
ORDER BY created_at ASC
LIMIT 1;
