-- +goose Up
ALTER TABLE schedule
  ADD COLUMN auto_pause_after   int,
  ADD COLUMN auto_paused_at     timestamptz,
  ADD COLUMN auto_pause_reason  text,
  ADD COLUMN last_enabled_at    timestamptz NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE schedule
  DROP COLUMN auto_pause_after,
  DROP COLUMN auto_paused_at,
  DROP COLUMN auto_pause_reason,
  DROP COLUMN last_enabled_at;
