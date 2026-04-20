-- +goose Up
CREATE TABLE schedule (
    id                  uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id              uuid NOT NULL,
    skill_id            uuid NOT NULL REFERENCES skill(id) ON DELETE CASCADE,
    name                text NOT NULL,
    cron                text NOT NULL,
    timezone            text NOT NULL DEFAULT 'UTC',
    overlap_policy      text NOT NULL DEFAULT 'skip' CHECK (overlap_policy IN ('skip','queue','concurrent')),
    timeout_sec         int NOT NULL DEFAULT 600,
    enabled             boolean NOT NULL DEFAULT true,
    provider            text NOT NULL,
    model               text NOT NULL,
    llm_secret_ref      text,
    llm_endpoint        text,
    llm_deployment      text,
    destinations_json   jsonb NOT NULL,
    writeback_json      jsonb,
    env_json            jsonb NOT NULL DEFAULT '{}'::jsonb,
    next_fire_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (skill_id, name)
);

CREATE INDEX schedule_enabled_next_fire_idx
    ON schedule (next_fire_at)
    WHERE enabled = true;

-- +goose Down
DROP TABLE schedule;
