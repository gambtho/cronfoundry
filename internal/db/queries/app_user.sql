-- name: UpsertUserOnLogin :one
INSERT INTO app_user (org_id, github_login, role, last_login_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (org_id, github_login) DO UPDATE
  SET last_login_at = now()
RETURNING *;

-- name: GetUserRole :one
SELECT role
FROM app_user
WHERE org_id = $1 AND github_login = $2;

-- name: ListUsers :many
SELECT *
FROM app_user
WHERE org_id = $1
ORDER BY github_login ASC;

-- name: CreateUser :one
INSERT INTO app_user (org_id, github_login, role)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, github_login) DO NOTHING
RETURNING *;

-- name: SetUserRole :exec
UPDATE app_user
SET role = $3
WHERE org_id = $1 AND github_login = $2;

-- name: DeleteUser :execrows
DELETE FROM app_user
WHERE org_id = $1 AND github_login = $2;

-- name: DeleteUserIfNotLast :execrows
-- Atomic guard for the last-user invariant. The subquery evaluates within
-- the same statement, so two concurrent admin deletes can't both observe
-- count=2 and both succeed — at most one wins, the loser sees affected=0.
DELETE FROM app_user AS u
WHERE u.org_id = $1
  AND u.github_login = $2
  AND (SELECT count(*) FROM app_user AS u2 WHERE u2.org_id = $1) > 1;

-- name: CountUsers :one
SELECT count(*) AS total FROM app_user WHERE org_id = $1;

-- name: CountAdmins :one
SELECT count(*) AS total FROM app_user WHERE org_id = $1 AND role = 'admin';
