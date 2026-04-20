-- +goose Up
--
-- NOTE: these are single-column FKs to organization(id). They prevent orphan
-- rows but don't enforce same-org relationships between children (e.g.,
-- schedule.org_id matching its skill.org_id). Composite FKs like
--   FOREIGN KEY (org_id, skill_id) REFERENCES skill(org_id, id)
-- would enforce that — requiring UNIQUE (org_id, id) indexes on each parent
-- and coordinated drop/re-add of the existing ON DELETE CASCADE FKs. That
-- hardening is deferred to a future migration once the full multi-tenant
-- surface is exercised; the MVP is single-tenant so the current constraints
-- are sufficient.

-- Retrofit missing org_id FKs. In the original migrations, only
-- repo_connection declared REFERENCES organization(id). The others relied on
-- transitive cascades (skill→repo_connection→organization, schedule→skill→...)
-- or had no parent-cascade path at all (secret, audit_log). Add direct FKs
-- so deletions cascade cleanly and orphan rows can't be inserted.

ALTER TABLE skill
    ADD CONSTRAINT skill_org_id_fkey
    FOREIGN KEY (org_id) REFERENCES organization(id) ON DELETE CASCADE;

ALTER TABLE schedule
    ADD CONSTRAINT schedule_org_id_fkey
    FOREIGN KEY (org_id) REFERENCES organization(id) ON DELETE CASCADE;

ALTER TABLE run
    ADD CONSTRAINT run_org_id_fkey
    FOREIGN KEY (org_id) REFERENCES organization(id) ON DELETE CASCADE;

ALTER TABLE secret
    ADD CONSTRAINT secret_org_id_fkey
    FOREIGN KEY (org_id) REFERENCES organization(id) ON DELETE CASCADE;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_org_id_fkey
    FOREIGN KEY (org_id) REFERENCES organization(id) ON DELETE CASCADE;

-- Enforce fire_reason/fire_time consistency. Scheduled runs must carry a
-- fire_time (so the partial unique index prevents duplicate ticks); manual
-- runs must have NULL (so the partial index doesn't collide across them).
ALTER TABLE run
    ADD CONSTRAINT run_fire_reason_time_consistent
    CHECK (
        (fire_reason = 'manual'   AND fire_time IS NULL) OR
        (fire_reason = 'schedule' AND fire_time IS NOT NULL)
    );

-- +goose Down
ALTER TABLE run            DROP CONSTRAINT run_fire_reason_time_consistent;
ALTER TABLE audit_log      DROP CONSTRAINT audit_log_org_id_fkey;
ALTER TABLE secret         DROP CONSTRAINT secret_org_id_fkey;
ALTER TABLE run            DROP CONSTRAINT run_org_id_fkey;
ALTER TABLE schedule       DROP CONSTRAINT schedule_org_id_fkey;
ALTER TABLE skill          DROP CONSTRAINT skill_org_id_fkey;
