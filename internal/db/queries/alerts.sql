-- name: ListSchedulesForQuietCheck :many
SELECT s.id, s.name, s.cron, s.timezone,
       (SELECT MAX(r.finished_at) FROM run r
         WHERE r.schedule_id = s.id AND r.status = 'succeeded')::timestamptz AS last_success
  FROM schedule s
 WHERE s.org_id = $1
   AND s.enabled = true
   AND s.auto_paused_at IS NULL
 ORDER BY s.name ASC;

-- name: ListRecentAutoPaused :many
-- Cutoff is computed by the caller using its injected clock so tests can
-- pin time deterministically. Production callers pass time.Now().Add(-7d).
SELECT id, name, auto_paused_at, auto_pause_reason
  FROM schedule
 WHERE org_id = $1
   AND auto_paused_at IS NOT NULL
   AND auto_paused_at > sqlc.arg(cutoff)::timestamptz
 ORDER BY auto_paused_at DESC
 LIMIT 20;
