package template

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRender_AllVariables(t *testing.T) {
	ctx := Context{
		Output:    "full output line 1\nfull output line 2",
		RunID:     "run-abc",
		RunDate:   "2026-04-19",
		StartedAt: time.Date(2026, 4, 19, 9, 0, 0, 0, time.UTC),
		Schedule:  Meta{Name: "monday-morning"},
		Skill:     Meta{Name: "weekly-digest"},
	}

	cases := map[string]string{
		`{{ output }}`:               "full output line 1\nfull output line 2",
		`{{ output.truncated 10 }}`:  "full outp…",
		`{{ run.id }}`:               "run-abc",
		`{{ run.date }}`:             "2026-04-19",
		`{{ run.started_at }}`:       "2026-04-19T09:00:00Z",
		`{{ schedule.name }}`:        "monday-morning",
		`{{ skill.name }}`:           "weekly-digest",
		`prefix {{ run.id }} suffix`: "prefix run-abc suffix",
	}
	for tmpl, want := range cases {
		got, warnings := Render(tmpl, ctx)
		assert.Equal(t, want, got, "template %q", tmpl)
		assert.Empty(t, warnings, "unexpected warnings for %q", tmpl)
	}
}

func TestRender_TruncatedNoop(t *testing.T) {
	ctx := Context{Output: "short"}
	out, w := Render(`{{ output.truncated 100 }}`, ctx)
	assert.Equal(t, "short", out)
	assert.Empty(t, w)
}

func TestRender_UnknownVariable(t *testing.T) {
	out, warnings := Render(`hello {{ mystery }}`, Context{})
	assert.Equal(t, `hello {{ mystery }}`, out)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "mystery")
}
