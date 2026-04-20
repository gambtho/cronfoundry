-- name: UpsertSkill :one
INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (repo_id, path) DO UPDATE
  SET name             = EXCLUDED.name,
      current_sha      = EXCLUDED.current_sha,
      frontmatter_json = EXCLUDED.frontmatter_json,
      updated_at       = now()
RETURNING *;

-- name: ListSkillsByRepo :many
SELECT *
FROM skill
WHERE repo_id = $1
ORDER BY path;

-- name: DeleteMissingSkills :exec
-- Removes skill rows under `repo_id` whose path is NOT in the given slice.
-- Cascades to schedule rows.
DELETE FROM skill
WHERE repo_id = $1
  AND NOT (path = ANY($2::text[]));
