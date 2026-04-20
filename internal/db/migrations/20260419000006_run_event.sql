-- +goose Up
CREATE TABLE run_event (
    id            bigserial PRIMARY KEY,
    run_id        uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    ts            timestamptz NOT NULL DEFAULT now(),
    level         text NOT NULL CHECK (level IN ('info','warn','error')),
    event_type    text NOT NULL,
    payload_json  jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX run_event_run_ts_idx ON run_event (run_id, ts);

-- +goose Down
DROP TABLE run_event;
