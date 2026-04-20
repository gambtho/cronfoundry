-- +goose Up
CREATE TABLE repo_connection (
    id                     uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id                 uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    github_app_install_id  bigint NOT NULL,
    owner                  text NOT NULL,
    name                   text NOT NULL,
    default_branch         text NOT NULL DEFAULT 'main',
    sync_interval_sec      int NOT NULL DEFAULT 60,
    last_synced_at         timestamptz,
    last_synced_head_sha   text,
    last_sync_error        text,
    created_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, owner, name)
);

-- +goose Down
DROP TABLE repo_connection;
