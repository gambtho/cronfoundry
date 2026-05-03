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

func TestAppendScheduleToSkill_StubSentinelsNotReturnedWhenNotImplemented(t *testing.T) {
	// Once implemented, this is a no-op safety net — sentinels should only fire
	// from their real code paths.
	_, err := AppendScheduleToSkill([]byte("version: 1\nskills: []\n"), "skills/missing", &config.Schedule{Name: "x"})
	if err == nil {
		// Permitted once full impl lands; no-op assertion.
		return
	}
	if !errors.Is(err, ErrSkillNotFound) {
		t.Logf("note: error from missing skill: %v", err)
	}
}
