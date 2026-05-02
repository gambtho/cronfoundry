package webapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// SQLC-generated row structs have no JSON tags, so encoding them produces
// PascalCase keys ("Status", "StartedAt") that the React SPA can't parse.
// Each row→DTO mapper must produce the snake_case shape the SPA expects.
// These tests lock down the wire contract for every list/detail handler.
//
// Whenever a SPA type in web/src/lib/types.ts changes, the corresponding
// test below should be updated alongside the DTO.

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func assertHasAndLacks(t *testing.T, body string, has []string, lacks []string) {
	t.Helper()
	for _, k := range has {
		if !strings.Contains(body, `"`+k+`":`) {
			t.Errorf("body missing key %q\n  body=%s", k, body)
		}
	}
	for _, k := range lacks {
		if strings.Contains(body, `"`+k+`":`) {
			t.Errorf("body has unwanted key %q (sqlc PascalCase leak?)\n  body=%s", k, body)
		}
	}
}

func tsValid(s string) pgtype.Timestamptz {
	t, _ := time.Parse(time.RFC3339, s)
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TestRunSummaryDTO_WireShape(t *testing.T) {
	row := dbgen.ListRunsForOrgRow{
		ID:           pgtype.UUID{Valid: true},
		Status:       "succeeded",
		FireReason:   "schedule",
		Actor:        nil,
		StartedAt:    tsValid("2026-05-02T17:00:00Z"),
		FinishedAt:   tsValid("2026-05-02T17:00:30Z"),
		DurationMs:   nil,
		ErrorKind:    nil,
		ErrorMsg:     nil,
		CreatedAt:    tsValid("2026-05-02T17:00:00Z"),
		ScheduleName: "every-5",
		SkillPath:    "skills/smoke",
		Owner:        "gambtho",
		RepoName:     "skills",
	}
	body := mustMarshal(t, runSummaryRowToDTO(row))
	assertHasAndLacks(t,
		body,
		[]string{"id", "status", "fire_reason", "started_at", "finished_at", "duration_ms",
			"error_kind", "error_msg", "created_at", "schedule_name", "skill_path", "owner", "repo_name"},
		[]string{"ID", "Status", "FireReason", "StartedAt", "ScheduleName"},
	)
}

func TestRunDetailDTO_WireShape(t *testing.T) {
	row := dbgen.GetRunForAdminRow{
		ID:           pgtype.UUID{Valid: true},
		Status:       "succeeded",
		FireReason:   "schedule",
		StartedAt:    tsValid("2026-05-02T17:00:00Z"),
		CreatedAt:    tsValid("2026-05-02T17:00:00Z"),
		ScheduleName: "every-5",
		SkillPath:    "skills/smoke",
		Owner:        "gambtho",
		RepoName:     "skills",
	}
	body := mustMarshal(t, runDetailRowToDTO(row))
	assertHasAndLacks(t,
		body,
		[]string{"tokens_in", "tokens_out", "cost_cents", "writeback_commit_sha",
			"status", "schedule_name", "started_at"},
		[]string{"TokensIn", "TokensOut", "WritebackCommitSha"},
	)
}

func TestRepoConnectionDTO_WireShape(t *testing.T) {
	row := dbgen.RepoConnection{
		ID:                 pgtype.UUID{Valid: true},
		OrgID:              pgtype.UUID{Valid: true},
		GithubAppInstallID: 12345,
		Owner:              "gambtho",
		Name:               "skills",
		DefaultBranch:      "main",
		SyncIntervalSec:    300,
		CreatedAt:          tsValid("2026-05-02T17:00:00Z"),
	}
	body := mustMarshal(t, repoToDTO(row))
	assertHasAndLacks(t,
		body,
		[]string{"id", "org_id", "github_app_install_id", "owner", "name",
			"default_branch", "sync_interval_sec", "last_synced_at", "last_sync_error", "created_at"},
		[]string{"ID", "OrgID", "GithubAppInstallID", "DefaultBranch"},
	)
}

func TestRunEventDTO_WireShape(t *testing.T) {
	row := dbgen.RunEvent{
		ID:          42,
		RunID:       pgtype.UUID{Valid: true},
		Ts:          tsValid("2026-05-02T17:00:00Z"),
		Level:       "info",
		EventType:   "run.started",
		PayloadJson: []byte(`{"foo":"bar"}`),
	}
	body := mustMarshal(t, runEventToDTO(row))
	assertHasAndLacks(t,
		body,
		[]string{"id", "run_id", "ts", "level", "event_type", "payload_json"},
		[]string{"ID", "RunID", "EventType", "PayloadJson"},
	)
	// payload_json must be raw JSON, not a base64 string
	if !strings.Contains(body, `"payload_json":{"foo":"bar"}`) {
		t.Errorf("payload_json should be raw JSON; body=%s", body)
	}
}

func TestRunEventDTO_EmptyPayloadIsNull(t *testing.T) {
	row := dbgen.RunEvent{
		ID:          1,
		RunID:       pgtype.UUID{Valid: true},
		Ts:          tsValid("2026-05-02T17:00:00Z"),
		Level:       "info",
		EventType:   "run.started",
		PayloadJson: nil,
	}
	body := mustMarshal(t, runEventToDTO(row))
	if !strings.Contains(body, `"payload_json":null`) {
		t.Errorf("nil payload should serialize to null; body=%s", body)
	}
}

func TestSkillDTO_WireShape(t *testing.T) {
	row := dbgen.ListSkillsByOrgRow{
		ID:         pgtype.UUID{Valid: true},
		RepoID:     pgtype.UUID{Valid: true},
		Path:       "skills/smoke",
		Name:       "smoke",
		CurrentSha: "abc123",
		UpdatedAt:  tsValid("2026-05-02T17:00:00Z"),
		Owner:      "gambtho",
		RepoName:   "skills",
	}
	body := mustMarshal(t, skillRowToDTO(row))
	assertHasAndLacks(t,
		body,
		[]string{"id", "repo_id", "path", "name", "current_sha",
			"updated_at", "owner", "repo_name"},
		// SPA's Skill type has no org_id or frontmatter_json — verify they
		// don't leak.
		[]string{"OrgID", "org_id", "FrontmatterJson", "frontmatter_json"},
	)
}
