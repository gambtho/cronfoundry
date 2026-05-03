package yamledit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gambtho/cronfoundry/internal/config"
)

// fixture loads input + expected from testdata/<name>/{input,expected}.yaml.
func fixture(t *testing.T, name string) (input, expected []byte) {
	t.Helper()
	in, err := os.ReadFile(filepath.Join("testdata", name, "input.yaml"))
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	exp, err := os.ReadFile(filepath.Join("testdata", name, "expected.yaml"))
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}
	return in, exp
}

func TestAppendScheduleToSkill_AppendToExisting(t *testing.T) {
	in, expected := fixture(t, "append_to_existing")

	sched := &config.Schedule{
		Name:     "hourly-pulse",
		Cron:     "0 * * * *",
		Timezone: "UTC",
		Provider: "copilot-enterprise",
		Model:    "gpt-5-mini",
		Destinations: []config.Destination{
			{
				GitHubIssue: &config.GitHubIssueDest{
					Repo:  "gambtho/skills",
					Title: "pulse",
				},
			},
		},
	}

	got, err := AppendScheduleToSkill(in, "skills/smoke", sched)
	if err != nil {
		t.Fatalf("AppendScheduleToSkill: %v", err)
	}
	if string(got) != string(expected) {
		t.Fatalf("output mismatch.\n--- got ---\n%s\n--- expected ---\n%s", got, expected)
	}

	// Belt-and-suspenders: the produced YAML must re-parse via config.ParseManifest.
	if _, err := config.ParseManifest(got); err != nil {
		t.Fatalf("output failed to ParseManifest: %v", err)
	}
}

func TestAppendScheduleToSkill_AppendFirstSchedule(t *testing.T) {
	in, expected := fixture(t, "append_first_schedule")
	sched := &config.Schedule{
		Name:     "hello",
		Cron:     "*/5 * * * *",
		Timezone: "UTC",
		Provider: "copilot-enterprise",
		Model:    "gpt-5-mini",
		Destinations: []config.Destination{
			{GitHubIssue: &config.GitHubIssueDest{Repo: "gambtho/skills", Title: "hello"}},
		},
	}
	got, err := AppendScheduleToSkill(in, "skills/empty", sched)
	if err != nil {
		t.Fatalf("AppendScheduleToSkill: %v", err)
	}
	if string(got) != string(expected) {
		t.Fatalf("mismatch\n---got---\n%s\n---want---\n%s", got, expected)
	}
	if _, err := config.ParseManifest(got); err != nil {
		t.Fatalf("ParseManifest on output: %v", err)
	}
}

func TestAppendScheduleToSkill_SkillNotFound(t *testing.T) {
	in := []byte(`version: 1
skills:
  - path: skills/exists
`)
	_, err := AppendScheduleToSkill(in, "skills/nope", &config.Schedule{Name: "x"})
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("want ErrSkillNotFound, got %v", err)
	}
}

func TestAppendScheduleToSkill_DuplicateName(t *testing.T) {
	in := []byte(`version: 1
skills:
  - path: skills/dup
    schedules:
      - name: same
        cron: "0 0 * * *"
        timezone: UTC
        provider: copilot-enterprise
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: x/y
              title: t
`)
	_, err := AppendScheduleToSkill(in, "skills/dup", &config.Schedule{Name: "same"})
	if !errors.Is(err, ErrDuplicateScheduleName) {
		t.Fatalf("want ErrDuplicateScheduleName, got %v", err)
	}
}
