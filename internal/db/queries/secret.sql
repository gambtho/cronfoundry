-- name: UpsertSecret :one
INSERT INTO secret (org_id, name, dek_wrapped, ciphertext, nonce, version, updated_at)
VALUES ($1, $2, $3, $4, $5, 1, now())
ON CONFLICT (org_id, name) DO UPDATE
  SET dek_wrapped = EXCLUDED.dek_wrapped,
      ciphertext  = EXCLUDED.ciphertext,
      nonce       = EXCLUDED.nonce,
      version     = secret.version + 1,
      updated_at  = now()
RETURNING *;

-- name: GetSecretByName :one
SELECT *
FROM secret
WHERE org_id = $1 AND name = $2;

-- name: TouchSecret :exec
UPDATE secret
SET last_used_at = now()
WHERE id = $1;

-- name: ListSecretNames :many
SELECT name, version, created_at, updated_at, last_used_at
FROM secret
WHERE org_id = $1
ORDER BY name ASC;

-- name: DeleteSecret :execrows
DELETE FROM secret
WHERE org_id = $1 AND name = $2;
