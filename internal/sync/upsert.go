package sync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/config"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// UpsertSkillsAndSchedules reconciles the parsed manifest + skill frontmatters
// against the DB:
//   - skills present in the manifest are upserted (keyed on repo_id+path)
//   - skills absent from the manifest are deleted (cascades to their schedules)
//   - for each manifest skill, schedules absent from the manifest are
//     soft-disabled (enabled=false) to preserve run history; schedules
//     present are upserted and marked enabled.
//
// All DB work happens in a single transaction.
func UpsertSkillsAndSchedules(
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, repoID pgtype.UUID,
	manifest *config.Manifest,
	skills map[string]*config.Skill,
	sha string,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sync: upsert: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbgen.New(tx)

	// 1) Upsert skills in the manifest; collect their IDs and paths.
	presentPaths := make([]string, 0, len(manifest.Skills))
	skillByPath := make(map[string]pgtype.UUID, len(manifest.Skills))
	for _, entry := range manifest.Skills {
		sk, ok := skills[entry.Path]
		if !ok {
			return fmt.Errorf("sync: upsert: missing SKILL.md for %q", entry.Path)
		}
		fmBytes, err := json.Marshal(sk.Frontmatter)
		if err != nil {
			return fmt.Errorf("sync: upsert: marshal frontmatter for %q: %w", entry.Path, err)
		}
		row, err := q.UpsertSkill(ctx, dbgen.UpsertSkillParams{
			OrgID:           orgID,
			RepoID:          repoID,
			Path:            entry.Path,
			Name:            sk.Frontmatter.Name,
			CurrentSha:      sha,
			FrontmatterJson: fmBytes,
		})
		if err != nil {
			return fmt.Errorf("sync: upsert skill %q: %w", entry.Path, err)
		}
		presentPaths = append(presentPaths, entry.Path)
		skillByPath[entry.Path] = row.ID
	}

	// 2) Delete skill rows no longer present (cascades to schedules).
	if err := q.DeleteMissingSkills(ctx, dbgen.DeleteMissingSkillsParams{
		RepoID:  repoID,
		Column2: presentPaths,
	}); err != nil {
		return fmt.Errorf("sync: delete missing skills: %w", err)
	}

	// 3) Per-skill: upsert schedules, disable missing.
	for _, entry := range manifest.Skills {
		skillID := skillByPath[entry.Path]

		presentNames := make([]string, 0, len(entry.Schedules))
		for _, sch := range entry.Schedules {
			destBytes, err := json.Marshal(sch.Destinations)
			if err != nil {
				return fmt.Errorf("sync: marshal destinations for %q/%q: %w", entry.Path, sch.Name, err)
			}
			var writebackBytes []byte
			if sch.Writeback != nil {
				writebackBytes, err = json.Marshal(sch.Writeback)
				if err != nil {
					return fmt.Errorf("sync: marshal writeback for %q/%q: %w", entry.Path, sch.Name, err)
				}
			}
			envBytes, err := json.Marshal(sch.Env)
			if err != nil {
				return fmt.Errorf("sync: marshal env for %q/%q: %w", entry.Path, sch.Name, err)
			}

			overlap := sch.EffectiveOverlapPolicy()
			timeoutSec := sch.TimeoutSec
			if timeoutSec == 0 {
				timeoutSec = 600
			}
			timezone := sch.Timezone
			if timezone == "" {
				timezone = "UTC"
			}

			if _, err := q.UpsertSchedule(ctx, dbgen.UpsertScheduleParams{
				OrgID:         orgID,
				SkillID:       skillID,
				Name:          sch.Name,
				Cron:          sch.Cron,
				Timezone:      timezone,
				OverlapPolicy: overlap,
				TimeoutSec:    int32(timeoutSec),
				Enabled:       true,
				Provider:      sch.Provider,
				Model:         sch.Model,
				// sqlc emits *string for nullable text columns
				// (emit_pointers_for_null_types: true). nil represents NULL.
				LlmSecretRef:     nil,
				LlmEndpoint:      nil,
				LlmDeployment:    nil,
				DestinationsJson: destBytes,
				WritebackJson:    writebackBytes,
				EnvJson:          envBytes,
			}); err != nil {
				return fmt.Errorf("sync: upsert schedule %q/%q: %w", entry.Path, sch.Name, err)
			}
			presentNames = append(presentNames, sch.Name)
		}

		if err := q.DisableMissingSchedules(ctx, dbgen.DisableMissingSchedulesParams{
			SkillID: skillID,
			Column2: presentNames,
		}); err != nil {
			return fmt.Errorf("sync: disable missing schedules for %q: %w", entry.Path, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sync: upsert: commit: %w", err)
	}
	return nil
}
