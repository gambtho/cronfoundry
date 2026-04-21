-- +goose Up

-- GitHub logins are case-insensitive identifiers but case-preserving in the
-- API response. Using citext keeps "Alice" entered at bootstrap and "alice"
-- returned from the OAuth /user endpoint as the same row, without scattering
-- lower() calls across every query or silently splitting roles by casing.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE app_user (
    id             uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    github_login   citext NOT NULL,
    role           text NOT NULL CHECK (role IN ('admin', 'viewer')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz,
    UNIQUE (org_id, github_login)
);

CREATE INDEX app_user_org_role_idx ON app_user (org_id, role);

-- +goose Down
DROP TABLE app_user;
-- Leave citext in place on downgrade — dropping an extension other tables
-- might use is riskier than leaving it installed.
