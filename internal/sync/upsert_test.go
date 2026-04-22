package sync

import (
	"context"
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
