-- +goose Up
CREATE TABLE run_notification (
    id          bigserial   PRIMARY KEY,
    run_id      uuid        NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    org_id      uuid        NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    kind        text        NOT NULL,
    target      text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('sent','skipped','failed')),
    reason      text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_notification_run_idx ON run_notification (run_id, id);
CREATE INDEX run_notification_org_idx ON run_notification (org_id, created_at DESC);

-- +goose Down
DROP TABLE run_notification;
