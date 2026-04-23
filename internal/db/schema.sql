-- internal/db/schema.sql
-- Concatenation of every migrations/*.sql up-block, in order, for sqlc
-- introspection only. Do not run this file directly — the authoritative
-- schema is applied by goose from internal/db/migrations/*.sql. Rebuild
-- with `make schema` when migrations change.

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE organization (
    id          uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

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

CREATE TABLE skill (
    id                uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id            uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    repo_id           uuid NOT NULL REFERENCES repo_connection(id) ON DELETE CASCADE,
    path              text NOT NULL,
    name              text NOT NULL,
    current_sha       text NOT NULL,
    frontmatter_json  jsonb NOT NULL,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repo_id, path)
);

CREATE TABLE schedule (
    id                  uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id              uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    skill_id            uuid NOT NULL REFERENCES skill(id) ON DELETE CASCADE,
    name                text NOT NULL,
    cron                text NOT NULL,
    timezone            text NOT NULL DEFAULT 'UTC',
    overlap_policy      text NOT NULL DEFAULT 'skip',
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
    auto_pause_after    int,
    auto_paused_at      timestamptz,
    auto_pause_reason   text,
    last_enabled_at     timestamptz NOT NULL DEFAULT now(),
    mcp_env_json        jsonb NOT NULL DEFAULT '{}'::jsonb,
    ui_overrides_json   jsonb NOT NULL DEFAULT '{}'::jsonb,
    max_turns           int,
    next_fire_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (skill_id, name)
);

CREATE TABLE run (
    id                    uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id                uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    schedule_id           uuid NOT NULL REFERENCES schedule(id) ON DELETE CASCADE,
    skill_sha             text NOT NULL,
    fire_time             timestamptz,
    status                text NOT NULL,
    fire_reason           text NOT NULL,
    actor                 text,
    started_at            timestamptz,
    finished_at           timestamptz,
    duration_ms           int,
    tokens_in             int,
    tokens_out            int,
    cost_cents            int,
    error_kind            text,
    error_msg             text,
    writeback_commit_sha  text,
    runner_pid            int,
    runner_token_hash     text NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT run_fire_reason_time_consistent CHECK (
        (fire_reason = 'manual'   AND fire_time IS NULL) OR
        (fire_reason = 'schedule' AND fire_time IS NOT NULL)
    )
);

CREATE TABLE run_event (
    id            bigserial PRIMARY KEY,
    run_id        uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    ts            timestamptz NOT NULL DEFAULT now(),
    level         text NOT NULL,
    event_type    text NOT NULL,
    payload_json  jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE secret (
    id           uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    name         text NOT NULL,
    dek_wrapped  bytea NOT NULL,
    ciphertext   bytea NOT NULL,
    nonce        bytea NOT NULL,
    version      int NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (org_id, name)
);

CREATE TABLE audit_log (
    id           bigserial PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    actor        text,
    action       text NOT NULL,
    target_kind  text,
    target_id    uuid,
    ts           timestamptz NOT NULL DEFAULT now(),
    detail_json  jsonb NOT NULL DEFAULT '{}'::jsonb
);

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
