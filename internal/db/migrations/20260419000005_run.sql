-- +goose Up
CREATE TABLE run (
    id                    uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id                uuid NOT NULL,
    schedule_id           uuid NOT NULL REFERENCES schedule(id) ON DELETE CASCADE,
    skill_sha             text NOT NULL,
    fire_time             timestamptz,   -- NULL for manual fires
    status                text NOT NULL CHECK (status IN ('pending','running','succeeded','partial_failure','failed')),
    fire_reason           text NOT NULL CHECK (fire_reason IN ('schedule','manual')),
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
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- Idempotent ticks: two calls to INSERT run (..., fire_time=T) for the same schedule collapse to one.
-- NULL fire_times (manual fires) are always distinct under this constraint.
CREATE UNIQUE INDEX run_schedule_firetime_unique
    ON run (schedule_id, fire_time)
    WHERE fire_time IS NOT NULL;

CREATE INDEX run_schedule_created_idx
    ON run (schedule_id, created_at DESC);

CREATE INDEX run_active_idx
    ON run (status, created_at)
    WHERE status IN ('pending','running');

-- +goose Down
DROP TABLE run;
