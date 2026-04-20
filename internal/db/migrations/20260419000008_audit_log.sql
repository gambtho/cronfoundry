-- +goose Up
CREATE TABLE audit_log (
    id           bigserial PRIMARY KEY,
    org_id       uuid NOT NULL,
    actor        text,
    action       text NOT NULL,
    target_kind  text,
    target_id    uuid,
    ts           timestamptz NOT NULL DEFAULT now(),
    detail_json  jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_log_org_ts_idx ON audit_log (org_id, ts DESC);

-- +goose Down
DROP TABLE audit_log;
