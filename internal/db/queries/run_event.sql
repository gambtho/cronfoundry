-- name: InsertRunEvent :exec
INSERT INTO run_event (run_id, level, event_type, payload_json)
VALUES ($1, $2, $3, $4);

-- name: ListRunEvents :many
SELECT *
FROM run_event
WHERE run_id = $1
ORDER BY ts ASC, id ASC;

-- name: ListRunEventsSince :many
SELECT *
FROM run_event
WHERE run_id = $1
  AND id > $2
ORDER BY id ASC;
