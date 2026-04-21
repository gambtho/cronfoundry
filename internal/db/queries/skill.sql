-- name: UpsertSkill :one
INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (repo_id, path) DO UPDATE
  SET name             = EXCLUDED.name,
      current_sha      = EXCLUDED.current_sha,
      frontmatter_json = EXCLUDED.frontmatter_json,
      updated_at       = now()
RETURNING *;

-- name: ListSkillsByOrg :many
SELECT sk.*, rc.owner, rc.name AS repo_name
FROM skill sk
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE sk.org_id = $1
ORDER BY rc.owner, rc.name, sk.path;

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

-- name: GetRunRepoID :one
-- Returns the repo_id the given run is bound to, via its schedule → skill.
-- Used by the clone-url handler to enforce that a run can only fetch the
-- clone URL for its own repo.
SELECT sk.repo_id
FROM run r
JOIN schedule s ON s.id = r.schedule_id
JOIN skill sk   ON sk.id = s.skill_id
WHERE r.id = $1;
