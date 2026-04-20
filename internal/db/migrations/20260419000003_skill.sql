-- +goose Up
CREATE TABLE skill (
    id                uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id            uuid NOT NULL,
    repo_id           uuid NOT NULL REFERENCES repo_connection(id) ON DELETE CASCADE,
    path              text NOT NULL,
    name              text NOT NULL,
    current_sha       text NOT NULL,
    frontmatter_json  jsonb NOT NULL,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, path)
);

-- +goose Down
DROP TABLE skill;
