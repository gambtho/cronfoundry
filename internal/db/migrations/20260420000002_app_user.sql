-- +goose Up
CREATE TABLE app_user (
    id             uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    github_login   text NOT NULL,
    role           text NOT NULL CHECK (role IN ('admin', 'viewer')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz,
    UNIQUE (org_id, github_login)
);

CREATE INDEX app_user_org_role_idx ON app_user (org_id, role);

-- +goose Down
DROP TABLE app_user;
