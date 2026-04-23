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

func TestManifest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing version",
			yaml:    "skills: []",
			wantErr: "version",
		},
		{
			name:    "unsupported version",
			yaml:    "version: 2\nskills: []",
			wantErr: "version 2 not supported",
		},
		{
			name:    "duplicate skill path",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules: []\n  - path: a\n    schedules: []",
			wantErr: "duplicate skill path \"a\"",
		},
		{
			name:    "duplicate schedule name within skill",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", provider: openai, model: m }\n      - { name: x, cron: \"* * * * *\", provider: openai, model: m }",
			wantErr: "duplicate schedule name \"x\"",
		},
		{
			name:    "schedule missing provider",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", model: m }",
			wantErr: "provider",
		},
		{
			name:    "schedule missing model",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", provider: openai }",
			wantErr: "model",
		},
		{
			name:    "invalid overlap policy",
			yaml:    "version: 1\nskills:\n  - path: a\n    schedules:\n      - { name: x, cron: \"* * * * *\", provider: openai, model: m, overlap_policy: weird }",
			wantErr: "overlap_policy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.yaml))
			if err != nil {
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			err = m.Validate()
			require.Error(t, err, "expected validation error")
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestManifest_Validate_AcceptsMinimalValid(t *testing.T) {
	yaml := []byte(`
version: 1
skills:
  - path: skills/a
    schedules:
      - name: s1
        cron: "* * * * *"
        provider: openai
        model: gpt
`)
	m, err := ParseManifest(yaml)
	require.NoError(t, err)
	require.NoError(t, m.Validate())
}

func TestManifest_Validate_AcceptsEmptyOverlapPolicy(t *testing.T) {
	// Empty overlap_policy must validate (default to skip via accessor).
	m := &Manifest{
		Version: 1,
		Skills: []SkillEntry{{
			Path: "a",
			Schedules: []Schedule{{
				Name: "s", Cron: "* * * * *",
				Provider: "openai", Model: "m",
				OverlapPolicy: "",
			}},
		}},
	}
	assert.NoError(t, m.Validate())
}

func TestSchedule_EffectiveOverlapPolicy(t *testing.T) {
	cases := map[string]string{
		"":           "skip",
		"skip":       "skip",
		"queue":      "queue",
		"concurrent": "concurrent",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			s := &Schedule{OverlapPolicy: in}
			assert.Equal(t, want, s.EffectiveOverlapPolicy())
		})
	}
}

func TestManifest_FindSchedule(t *testing.T) {
	m := &Manifest{
		Version: 1,
		Skills: []SkillEntry{
			{Path: "skills/a", Schedules: []Schedule{{Name: "s1", Cron: "* * * * *", Provider: "openai", Model: "m"}}},
		},
	}
	require.NoError(t, m.Validate())

	skill, sch, err := m.FindSchedule("skills/a", "s1")
	require.NoError(t, err)
	assert.Equal(t, "skills/a", skill.Path)
	assert.Equal(t, "s1", sch.Name)

	_, _, err = m.FindSchedule("skills/a", "missing")
	assert.ErrorContains(t, err, "schedule \"missing\"")

	_, _, err = m.FindSchedule("skills/missing", "s1")
	assert.ErrorContains(t, err, "skill \"skills/missing\"")
}

func TestParseManifest_AutoPauseAfter(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want *int   // nil means the AutoPause field should be nil
		err  string // non-empty expected substring means expect a Validate() error
	}{
		{
			name: "missing auto_pause → nil",
			yaml: minimalManifest(""),
			want: nil,
		},
		{
			name: "auto_pause.after: 3",
			yaml: minimalManifest("        auto_pause:\n          after: 3\n"),
			want: intPtr(3),
		},
		{
			name: "auto_pause.after: 0 rejected",
			yaml: minimalManifest("        auto_pause:\n          after: 0\n"),
			err:  "auto_pause.after must be >= 1",
		},
		{
			name: "auto_pause.after: -1 rejected",
			yaml: minimalManifest("        auto_pause:\n          after: -1\n"),
			err:  "auto_pause.after must be >= 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.yaml))
			require.NoError(t, err)
			verr := m.Validate()
			if tc.err != "" {
				require.Error(t, verr)
				require.Contains(t, verr.Error(), tc.err)
				return
			}
			require.NoError(t, verr)
			sch := m.Skills[0].Schedules[0]
			if tc.want == nil {
				require.Nil(t, sch.AutoPause)
			} else {
				require.NotNil(t, sch.AutoPause)
				require.Equal(t, *tc.want, sch.AutoPause.After)
			}
		})
	}
}

func intPtr(i int) *int { return &i }

// minimalManifest returns a valid manifest YAML with the given extra lines
// spliced into the single schedule's body. Each extra line must already be
// indented to align with the schedule block.
func minimalManifest(extra string) string {
	return `version: 1
skills:
  - path: skills/hello
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: openai
        model: gpt-4o
` + extra
}

func TestParseManifest_MCPEnvAndMaxTurns(t *testing.T) {
	src := []byte(`version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday
        cron: "0 9 * * MON"
        provider: anthropic
        model: claude-opus-4-7
        max_turns: 40
        env:
          LOOKBACK_DAYS: "7"
        mcp_env:
          github:
            GITHUB_PERSONAL_ACCESS_TOKEN:
              secret: github_mcp_pat
          fetch: {}
`)
	m, err := ParseManifest(src)
	require.NoError(t, err)
	require.NoError(t, m.Validate())
	sch := m.Skills[0].Schedules[0]
	assert.Equal(t, 40, sch.MaxTurns)
	require.Contains(t, sch.MCPEnv, "github")
	require.Contains(t, sch.MCPEnv, "fetch")
	tok, ok := sch.MCPEnv["github"]["GITHUB_PERSONAL_ACCESS_TOKEN"]
	require.True(t, ok)
	assert.Equal(t, "github_mcp_pat", tok.Secret)
	// Empty server env map is valid.
	assert.Empty(t, sch.MCPEnv["fetch"])
}

func TestParseManifest_RejectsNegativeMaxTurns(t *testing.T) {
	src := []byte(`version: 1
skills:
  - path: skills/a
    schedules:
      - name: s
        cron: "* * * * *"
        provider: anthropic
        model: x
        max_turns: -1
`)
	m, err := ParseManifest(src)
	require.NoError(t, err)
	valErr := m.Validate()
	require.Error(t, valErr)
	assert.Contains(t, valErr.Error(), "max_turns must be between 0 and 2147483647")
}

func TestDestination_ShouldPublish(t *testing.T) {
	cases := []struct {
		when      string
		succeeded bool
		want      bool
	}{
		{"", true, true},
		{"", false, true},
		{"always", true, true},
		{"always", false, true},
		{"on_success", true, true},
		{"on_success", false, false},
		{"on_failure", true, false},
		{"on_failure", false, true},
	}
	for _, c := range cases {
		d := Destination{When: c.when}
		got := d.ShouldPublish(c.succeeded)
		if got != c.want {
			t.Errorf("when=%q succeeded=%v: want %v, got %v", c.when, c.succeeded, c.want, got)
		}
	}
}
