package sync

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/db"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// startPG brings up a throwaway Postgres, runs migrations, and seeds an org
// plus a repo_connection. Returns (pool, orgID, repoID, cleanup).

func startPG(t *testing.T) (*pgxpool.Pool, pgtype.UUID, pgtype.UUID, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf_test"),
		postgres.WithUsername("cf"),
		postgres.WithPassword("cf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, dsn))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('test-org') RETURNING id`).Scan(&orgID))

	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))

	return pool, orgID, repoID, func() { pool.Close(); _ = c.Terminate(context.Background()) }
}

func TestUpsertSkillsAndSchedules_FullRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	manifestYAML := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: hourly
        cron: "0 * * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	m, err := config.ParseManifest([]byte(manifestYAML))
	require.NoError(t, err)
	require.NoError(t, m.Validate())

	skills := map[string]*config.Skill{
		"skills/a": {
			Frontmatter: config.SkillFrontmatter{Name: "a"},
			Body:        "prompt",
		},
	}

	ctx := context.Background()
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m, skills, "sha-initial"))

	// Assert: one skill, one schedule, enabled.
	q := dbgen.New(pool)
	listed, err := q.ListSkillsByRepo(ctx, repoID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "skills/a", listed[0].Path)
	assert.Equal(t, "sha-initial", listed[0].CurrentSha)

	var schedName string
	var enabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT name, enabled FROM schedule WHERE skill_id = $1`, listed[0].ID).Scan(&schedName, &enabled))
	assert.Equal(t, "hourly", schedName)
	assert.True(t, enabled)

	// Second sync: drop `hourly`, add `daily`.
	manifestYAML2 := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	m2, _ := config.ParseManifest([]byte(manifestYAML2))
	require.NoError(t, m2.Validate())
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m2, skills, "sha-second"))

	var hourlyEnabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT enabled FROM schedule WHERE skill_id = $1 AND name = 'hourly'`, listed[0].ID).Scan(&hourlyEnabled))
	assert.False(t, hourlyEnabled, "removed schedule should be soft-disabled, not deleted")

	var dailyEnabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT enabled FROM schedule WHERE skill_id = $1 AND name = 'daily'`, listed[0].ID).Scan(&dailyEnabled))
	assert.True(t, dailyEnabled)
}

func TestUpsert_AutoPauseAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	skills := map[string]*config.Skill{
		"skills/a": {
			Frontmatter: config.SkillFrontmatter{Name: "a"},
			Body:        "prompt",
		},
	}

	ctx := context.Background()

	// First sync: schedule with auto_pause_after = 3.
	manifestWith := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
        auto_pause:
          after: 3
`
	m1, err := config.ParseManifest([]byte(manifestWith))
	require.NoError(t, err)
	require.NoError(t, m1.Validate())
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m1, skills, "sha1"))

	q := dbgen.New(pool)
	listed, err := q.ListSkillsByRepo(ctx, repoID)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	var autoPauseAfter *int32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT auto_pause_after FROM schedule WHERE skill_id = $1 AND name = 'daily'`, listed[0].ID).
		Scan(&autoPauseAfter))
	require.NotNil(t, autoPauseAfter)
	assert.Equal(t, int32(3), *autoPauseAfter)

	// Second sync: same schedule without auto_pause → auto_pause_after should become NULL.
	manifestWithout := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	m2, err := config.ParseManifest([]byte(manifestWithout))
	require.NoError(t, err)
	require.NoError(t, m2.Validate())
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m2, skills, "sha2"))

	var autoPauseAfterNull *int32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT auto_pause_after FROM schedule WHERE skill_id = $1 AND name = 'daily'`, listed[0].ID).
		Scan(&autoPauseAfterNull))
	assert.Nil(t, autoPauseAfterNull, "auto_pause_after should be NULL when AutoPause is nil")
}

// TestUpsert_RejectsMissingMCPEnv verifies that a skill declaring an
// mcp_servers entry whose name is not present in the schedule's mcp_env
// block is rejected at sync time.
func TestUpsert_RejectsMissingMCPEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name: "weekly-digest",
			MCPServers: []config.MCPServer{
				{Name: "github", Command: "npx"},
			},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "anthropic", Model: "claude-opus-4-7",
				// mcp_env is MISSING for declared 'github' server.
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp_env missing")
}

// TestUpsert_RejectsStrayMCPEnv verifies that a schedule's mcp_env block
// referencing a server that the skill never declared in mcp_servers is
// rejected.
func TestUpsert_RejectsStrayMCPEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name: "weekly-digest",
			MCPServers: []config.MCPServer{
				{Name: "github", Command: "npx"},
			},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "anthropic", Model: "claude-opus-4-7",
				MCPEnv: map[string]map[string]config.EnvValue{
					"github": {},
					// 'slack' is NOT declared by the skill.
					"slack": {},
				},
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undeclared server")
}

// TestUpsert_RejectsAzureFoundryWithMCPServers verifies that a skill
// declaring mcp_servers while the schedule targets azure-foundry is
// rejected — that provider does not yet support MCP.
func TestUpsert_RejectsAzureFoundryWithMCPServers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name: "weekly-digest",
			MCPServers: []config.MCPServer{
				{Name: "github", Command: "npx"},
			},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "azure-foundry", Model: "gpt-4o",
				MCPEnv: map[string]map[string]config.EnvValue{
					"github": {},
				},
			}},
		}},
	}
	err := UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azure-foundry")
}

// TestUpsert_PersistsMCPEnvAndMaxTurns verifies that a valid skill/schedule
// pair round-trips through the DB: mcp_env_json is stored as JSON and
// max_turns is stored as a non-null integer.
func TestUpsert_PersistsMCPEnvAndMaxTurns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	skill := &config.Skill{
		Frontmatter: config.SkillFrontmatter{
			Name: "weekly-digest",
			MCPServers: []config.MCPServer{
				{Name: "github", Command: "npx"},
			},
		},
	}
	manifest := &config.Manifest{
		Version: 1,
		Skills: []config.SkillEntry{{
			Path: "skills/weekly-digest",
			Schedules: []config.Schedule{{
				Name: "monday", Cron: "0 9 * * MON",
				Provider: "anthropic", Model: "claude-opus-4-7",
				MaxTurns: 25,
				MCPEnv: map[string]map[string]config.EnvValue{
					"github": {
						"GITHUB_TOKEN": {Secret: "gh_token"},
					},
				},
			}},
		}},
	}
	ctx := context.Background()
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/weekly-digest": skill}, "sha-abc"))

	var mcpEnvJSON []byte
	var maxTurns *int32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT mcp_env_json, max_turns FROM schedule WHERE name = 'monday'`).Scan(&mcpEnvJSON, &maxTurns))
	assert.Contains(t, string(mcpEnvJSON), "github")
	assert.Contains(t, string(mcpEnvJSON), "GITHUB_TOKEN")
	require.NotNil(t, maxTurns)
	assert.Equal(t, int32(25), *maxTurns)
}

func TestUpsertSkillsAndSchedules_CopilotTokenRefs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	manifestYAML := `version: 1
skills:
  - path: skills/cp
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: copilot-enterprise
        model: gpt-4o
        copilot_prefix: mycopilot
`
	manifest, err := config.ParseManifest([]byte(manifestYAML))
	require.NoError(t, err)

	err = UpsertSkillsAndSchedules(context.Background(), pool, orgID, repoID, manifest,
		map[string]*config.Skill{"skills/cp": {Frontmatter: config.SkillFrontmatter{Name: "cp"}}}, "abc123")
	require.NoError(t, err)

	q := dbgen.New(pool)
	rows, err := q.ListSchedulesByOrg(context.Background(), orgID)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	var refs struct {
		Prefix string `json:"prefix"`
	}
	require.NoError(t, json.Unmarshal(rows[0].CopilotTokenRefsJson, &refs))
	assert.Equal(t, "mycopilot", refs.Prefix)
}

// Regression: an UpsertSchedule that left next_fire_at NULL meant freshly
// synced schedules were invisible to the dispatcher (which selects WHERE
// next_fire_at IS NOT NULL AND next_fire_at <= now()), so brand-new installs
// never fired any cron-driven runs until something else wrote the column.
// This test pins the contract that:
//   1. inserting a schedule populates next_fire_at to the cron's next time,
//   2. a follow-up sync does NOT clobber an already-advanced next_fire_at
//      (the tick loop is the authoritative writer for armed schedules), and
//   3. a sync that re-enables a soft-disabled schedule re-arms it.
func TestUpsertSkillsAndSchedules_PopulatesNextFireAt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	// Pin "now" so we can assert the cron-derived next_fire_at exactly.
	pinned := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	prev := nowUTC
	nowUTC = func() time.Time { return pinned }
	defer func() { nowUTC = prev }()

	manifestYAML := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: hourly
        cron: "0 * * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	m, err := config.ParseManifest([]byte(manifestYAML))
	require.NoError(t, err)
	require.NoError(t, m.Validate())

	skills := map[string]*config.Skill{
		"skills/a": {Frontmatter: config.SkillFrontmatter{Name: "a"}},
	}

	ctx := context.Background()
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m, skills, "sha1"))

	var firstNF pgtype.Timestamptz
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT next_fire_at FROM schedule WHERE name = 'hourly'`).Scan(&firstNF))
	require.True(t, firstNF.Valid, "next_fire_at must be populated on insert; otherwise the dispatcher never sees the schedule")
	assert.Equal(t, time.Date(2026, 5, 4, 13, 0, 0, 0, time.UTC), firstNF.Time.UTC())

	// Simulate the dispatcher having advanced next_fire_at past the next slot.
	advanced := time.Date(2026, 5, 4, 14, 0, 0, 0, time.UTC)
	_, err = pool.Exec(ctx,
		`UPDATE schedule SET next_fire_at = $1 WHERE name = 'hourly'`, advanced)
	require.NoError(t, err)

	// Re-sync at a later "now". The advanced value must survive — sync is
	// not allowed to rewind an armed schedule.
	nowUTC = func() time.Time { return pinned.Add(time.Hour) }
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m, skills, "sha2"))

	var afterResync pgtype.Timestamptz
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT next_fire_at FROM schedule WHERE name = 'hourly'`).Scan(&afterResync))
	assert.Equal(t, advanced.UTC(), afterResync.Time.UTC(),
		"re-syncing must not clobber a dispatcher-advanced next_fire_at")

	// Soft-disable, then re-sync. The schedule should be re-armed using the
	// cron-derived value relative to the new "now" (pinned + 2h = 14:30Z,
	// next "0 * * * *" is 15:00Z).
	_, err = pool.Exec(ctx,
		`UPDATE schedule SET enabled = false WHERE name = 'hourly'`)
	require.NoError(t, err)
	nowUTC = func() time.Time { return pinned.Add(2 * time.Hour) }
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m, skills, "sha3"))

	var reArmed pgtype.Timestamptz
	var enabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT next_fire_at, enabled FROM schedule WHERE name = 'hourly'`).
		Scan(&reArmed, &enabled))
	assert.True(t, enabled, "re-syncing a manifest containing the schedule must re-enable it")
	assert.Equal(t, time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC), reArmed.Time.UTC(),
		"re-enable must re-arm next_fire_at from the new now()")
}
