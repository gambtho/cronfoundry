-- +goose Up
ALTER TABLE schedule
  ADD COLUMN mcp_env_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN max_turns    int;

-- +goose Down
ALTER TABLE schedule
  DROP COLUMN mcp_env_json,
  DROP COLUMN max_turns;
