-- +goose Up
ALTER TABLE schedule
  ADD COLUMN ui_overrides_json jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE schedule DROP COLUMN ui_overrides_json;
