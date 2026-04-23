-- +goose Up
ALTER TABLE schedule
  ADD COLUMN copilot_token_refs_json JSONB;

-- +goose Down
ALTER TABLE schedule
  DROP COLUMN copilot_token_refs_json;
