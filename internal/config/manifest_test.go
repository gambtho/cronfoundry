package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseManifest_HappyPath(t *testing.T) {
	yaml := []byte(`
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        timezone: America/Los_Angeles
        overlap_policy: skip
        timeout_sec: 600
        provider: openai
        model: gpt-5.1
        destinations:
          - github-issue:
              repo: myorg/reports
              title: "Weekly digest"
              labels: [digest, automated]
          - slack:
              secret: slack_digest_webhook
        writeback:
          enabled: true
          path: memory.md
          mode: append
        env:
          LOOKBACK_DAYS: "7"
          TEAM_NAME:
            secret: team_name
`)

	m, err := ParseManifest(yaml)

	require.NoError(t, err)
	assert.Equal(t, 1, m.Version)
	require.Len(t, m.Skills, 1)

	skill := m.Skills[0]
	assert.Equal(t, "skills/weekly-digest", skill.Path)
	require.Len(t, skill.Schedules, 1)

	sch := skill.Schedules[0]
	assert.Equal(t, "monday-morning", sch.Name)
	assert.Equal(t, "0 9 * * MON", sch.Cron)
	assert.Equal(t, "America/Los_Angeles", sch.Timezone)
	assert.Equal(t, "skip", sch.OverlapPolicy)
	assert.Equal(t, 600, sch.TimeoutSec)
	assert.Equal(t, "openai", sch.Provider)
	assert.Equal(t, "gpt-5.1", sch.Model)

	require.Len(t, sch.Destinations, 2)
	require.NotNil(t, sch.Destinations[0].GitHubIssue)
	assert.Equal(t, "myorg/reports", sch.Destinations[0].GitHubIssue.Repo)
	assert.Equal(t, []string{"digest", "automated"}, sch.Destinations[0].GitHubIssue.Labels)
	require.NotNil(t, sch.Destinations[1].Slack)
	assert.Equal(t, "slack_digest_webhook", sch.Destinations[1].Slack.Secret)

	require.NotNil(t, sch.Writeback)
	assert.True(t, sch.Writeback.Enabled)
	assert.Equal(t, "memory.md", sch.Writeback.Path)
	assert.Equal(t, "append", sch.Writeback.Mode)

	assert.Equal(t, "7", sch.Env["LOOKBACK_DAYS"].Literal)
	assert.Equal(t, "team_name", sch.Env["TEAM_NAME"].Secret)
}

func TestParseManifest_EnvValue_RejectsBadShapes(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		substr string
	}{
		{
			name:   "env value is boolean",
			yaml:   "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: s, cron: \"* * * * *\", provider: p, model: m, env: { X: true } }",
			substr: "env value must be a string or { secret: name }",
		},
		{
			name:   "env value is number",
			yaml:   "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: s, cron: \"* * * * *\", provider: p, model: m, env: { X: 7 } }",
			substr: "env value must be a string or { secret: name }",
		},
		{
			name:   "env value is null",
			yaml:   "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: s, cron: \"* * * * *\", provider: p, model: m, env: { X: ~ } }",
			substr: "env value must not be null",
		},
		{
			name:   "env object missing secret",
			yaml:   "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: s, cron: \"* * * * *\", provider: p, model: m, env: { X: { other: y } } }",
			substr: "must set 'secret'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.substr)
		})
	}
}

func TestParseManifest_RejectsUnknownFields(t *testing.T) {
	// "overlap-policy" (hyphen) is a typo of "overlap_policy".
	bad := []byte(`
version: 1
skills:
  - path: a
    schedules:
      - name: s
        cron: "* * * * *"
        provider: p
        model: m
        overlap-policy: skip
`)
	_, err := ParseManifest(bad)
	require.Error(t, err)
	// Depending on SDK, error phrasing may be "unknown field" or "unknown fields".
	// Just require that the field name appears in the error.
	assert.Contains(t, err.Error(), "overlap-policy")
}
