-- +goose Up
ALTER TABLE schedule
  ADD COLUMN mcp_env_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN max_turns    int;

ALTER TABLE schedule
  ADD CONSTRAINT check_schedule_max_turns_positive CHECK (max_turns IS NULL OR max_turns > 0);

-- +goose Down
ALTER TABLE schedule DROP CONSTRAINT IF EXISTS check_schedule_max_turns_positive;
ALTER TABLE schedule
  DROP COLUMN mcp_env_json,
  DROP COLUMN max_turns;
