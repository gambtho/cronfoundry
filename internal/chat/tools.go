package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/llm"
)

// Toolbox is the read-only tool surface exposed to the chat agent. It wraps
// the same sqlc Queries the rest of webapi uses, but only the safe-to-read
// subset and only filtered to the org the operator belongs to.
type Toolbox struct {
	Queries *dbgen.Queries
	OrgID   pgtype.UUID
}

// Defs returns the tool definitions in the format the LLM provider expects.
// Order is stable so model behavior is reproducible.
func (tb Toolbox) Defs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "list_jobs",
			Description: "List all scheduled jobs (schedules) for the operator's organization. Returns id, name, cron, timezone, enabled, provider, model, skill_path, owner, repo_name, next_fire_at, auto_paused_at for each.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
		{
			Name:        "get_recent_runs",
			Description: "Return the most recent runs across all jobs (or a specific schedule), newest first. Use this to triage failures.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"limit":{"type":"integer","minimum":1,"maximum":50,"description":"How many runs to return. Default 10."},
					"schedule_name":{"type":"string","description":"Optional: filter to runs of this schedule name."}
				},
				"required":[]
			}`),
		},
		{
			Name:        "get_run",
			Description: "Get full detail for a single run by id, including tokens, cost, error_kind, error_msg, and writeback commit.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"run_id":{"type":"string","description":"The run UUID."}
				},
				"required":["run_id"]
			}`),
		},
		{
			Name:        "get_run_events",
			Description: "Get the timeline of events for a run (run.started, llm.start, publish.*, writeback.*, run.finished, etc). Essential for diagnosing failures.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"run_id":{"type":"string"},
					"limit":{"type":"integer","minimum":1,"maximum":200,"description":"Cap event count. Default 50."}
				},
				"required":["run_id"]
			}`),
		},
		{
			Name:        "list_skills",
			Description: "List all known skills (parsed from connected repos).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
		{
			Name:        "list_secret_names",
			Description: "List the names of secrets currently in the secret store. Values are NEVER returned. Use this to confirm a manifest's secret references are present.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
		{
			Name:        "list_repos",
			Description: "List the GitHub repos connected to this CronFoundry instance.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
		{
			Name:        "get_alerts",
			Description: "Return current operator alerts: quiet jobs (haven't succeeded in their expected window) and recently auto-paused schedules.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
	}
}

// Call dispatches a single tool invocation. Returns the JSON-encoded tool
// result that will be fed back to the model. errors are returned to the
// caller (not stuffed into the result) so the agent loop can decide whether
// to surface them as tool errors or as fatal.
func (tb Toolbox) Call(ctx context.Context, caller Caller, name string, raw json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "list_jobs":
		return tb.listJobs(ctx)
	case "get_recent_runs":
		return tb.getRecentRuns(ctx, raw)
	case "get_run":
		return tb.getRun(ctx, raw)
	case "get_run_events":
		return tb.getRunEvents(ctx, raw)
	case "list_skills":
		return tb.listSkills(ctx)
	case "list_secret_names":
		// Secret names are not sensitive (they're already visible in the
		// operator's manifest), but we still gate behind any authenticated
		// session so anonymous users can't enumerate them via a hijacked
		// chat endpoint.
		if caller.Login == "" {
			return nil, errors.New("authentication required")
		}
		return tb.listSecretNames(ctx)
	case "list_repos":
		return tb.listRepos(ctx)
	case "get_alerts":
		return tb.getAlerts(ctx)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (tb Toolbox) listJobs(ctx context.Context) (json.RawMessage, error) {
	rows, err := tb.Queries.ListSchedulesByOrg(ctx, tb.OrgID)
	if err != nil {
		return nil, err
	}
	type out struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		Cron         string  `json:"cron"`
		Timezone     string  `json:"timezone"`
		Enabled      bool    `json:"enabled"`
		Provider     string  `json:"provider"`
		Model        string  `json:"model"`
		SkillPath    string  `json:"skill_path"`
		Owner        string  `json:"owner"`
		RepoName     string  `json:"repo_name"`
		NextFireAt   *string `json:"next_fire_at,omitempty"`
		AutoPausedAt *string `json:"auto_paused_at,omitempty"`
		AutoPauseRsn *string `json:"auto_pause_reason,omitempty"`
	}
	res := make([]out, 0, len(rows))
	for _, r := range rows {
		o := out{
			ID:        uuidStr(r.ID),
			Name:      r.Name,
			Cron:      r.Cron,
			Timezone:  r.Timezone,
			Enabled:   r.Enabled,
			Provider:  r.Provider,
			Model:     r.Model,
			SkillPath: r.SkillPath,
			Owner:     r.Owner,
			RepoName:  r.RepoName,
		}
		if r.NextFireAt.Valid {
			s := r.NextFireAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			o.NextFireAt = &s
		}
		if r.AutoPausedAt.Valid {
			s := r.AutoPausedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			o.AutoPausedAt = &s
		}
		if r.AutoPauseReason != nil {
			o.AutoPauseRsn = r.AutoPauseReason
		}
		res = append(res, o)
	}
	return json.Marshal(res)
}

func (tb Toolbox) getRecentRuns(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Limit        int    `json:"limit"`
		ScheduleName string `json:"schedule_name"`
	}
	// Treat an empty raw payload as "no args"; only error on actually-malformed
	// JSON. This keeps tool calls with no input from being rejected.
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("get_recent_runs: bad arguments: %w", err)
		}
	}
	if args.Limit <= 0 || args.Limit > 50 {
		args.Limit = 10
	}
	type out struct {
		ID           string  `json:"id"`
		Status       string  `json:"status"`
		ScheduleName string  `json:"schedule_name"`
		SkillPath    string  `json:"skill_path"`
		StartedAt    *string `json:"started_at,omitempty"`
		FinishedAt   *string `json:"finished_at,omitempty"`
		DurationMs   *int32  `json:"duration_ms,omitempty"`
		ErrorKind    *string `json:"error_kind,omitempty"`
		ErrorMsg     *string `json:"error_msg,omitempty"`
		FireReason   string  `json:"fire_reason"`
		Actor        *string `json:"actor,omitempty"`
	}
	// When a schedule_name filter is supplied, push it into the query so we
	// don't truncate to top-N globally and then filter (which can drop the
	// requested schedule entirely if its runs aren't in the latest page).
	if args.ScheduleName != "" {
		rows, err := tb.Queries.ListRunsForSchedule(ctx, dbgen.ListRunsForScheduleParams{
			OrgID: tb.OrgID,
			Name:  args.ScheduleName,
			Limit: int32(args.Limit),
		})
		if err != nil {
			return nil, err
		}
		res := make([]out, 0, len(rows))
		for _, r := range rows {
			res = append(res, out{
				ID:           uuidStr(r.ID),
				Status:       r.Status,
				ScheduleName: r.ScheduleName,
				SkillPath:    r.SkillPath,
				StartedAt:    tsPtr(r.StartedAt),
				FinishedAt:   tsPtr(r.FinishedAt),
				DurationMs:   r.DurationMs,
				ErrorKind:    r.ErrorKind,
				ErrorMsg:     r.ErrorMsg,
				FireReason:   r.FireReason,
				Actor:        r.Actor,
			})
		}
		return json.Marshal(res)
	}

	rows, err := tb.Queries.ListRunsForOrg(ctx, dbgen.ListRunsForOrgParams{
		OrgID: tb.OrgID,
		Limit: int32(args.Limit),
	})
	if err != nil {
		return nil, err
	}
	res := make([]out, 0, len(rows))
	for _, r := range rows {
		res = append(res, out{
			ID:           uuidStr(r.ID),
			Status:       r.Status,
			ScheduleName: r.ScheduleName,
			SkillPath:    r.SkillPath,
			StartedAt:    tsPtr(r.StartedAt),
			FinishedAt:   tsPtr(r.FinishedAt),
			DurationMs:   r.DurationMs,
			ErrorKind:    r.ErrorKind,
			ErrorMsg:     r.ErrorMsg,
			FireReason:   r.FireReason,
			Actor:        r.Actor,
		})
	}
	return json.Marshal(res)
}

func (tb Toolbox) getRun(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("bad arguments: %w", err)
	}
	id, err := uuid.Parse(args.RunID)
	if err != nil {
		return nil, fmt.Errorf("invalid run_id")
	}
	r, err := tb.Queries.GetRunForAdmin(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return json.RawMessage(`{"error":"run not found"}`), nil
		}
		return nil, err
	}
	if r.OrgID != tb.OrgID {
		// Cross-org isolation: don't leak rows from another org even if a
		// caller somehow guessed a UUID.
		return json.RawMessage(`{"error":"run not found"}`), nil
	}
	out := map[string]any{
		"id":            uuidStr(r.ID),
		"status":        r.Status,
		"schedule_name": r.ScheduleName,
		"skill_path":    r.SkillPath,
		"owner":         r.Owner,
		"repo_name":     r.RepoName,
		"started_at":    tsPtr(r.StartedAt),
		"finished_at":   tsPtr(r.FinishedAt),
		"duration_ms":   r.DurationMs,
		"tokens_in":     r.TokensIn,
		"tokens_out":    r.TokensOut,
		"cost_cents":    r.CostCents,
		"error_kind":    r.ErrorKind,
		"error_msg":     r.ErrorMsg,
		"writeback_sha": r.WritebackCommitSha,
		"fire_reason":   r.FireReason,
		"actor":         r.Actor,
	}
	return json.Marshal(out)
}

func (tb Toolbox) getRunEvents(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		RunID string `json:"run_id"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("bad arguments: %w", err)
	}
	if args.Limit <= 0 || args.Limit > 200 {
		args.Limit = 50
	}
	id, err := uuid.Parse(args.RunID)
	if err != nil {
		return nil, fmt.Errorf("invalid run_id")
	}
	pgID := pgtype.UUID{Bytes: id, Valid: true}

	// Cross-org check: load the run row first so we know it belongs to
	// this org. ListRunEvents has no org filter on its own.
	r, err := tb.Queries.GetRunForAdmin(ctx, pgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return json.RawMessage(`{"error":"run not found"}`), nil
		}
		return nil, err
	}
	if r.OrgID != tb.OrgID {
		return json.RawMessage(`{"error":"run not found"}`), nil
	}

	events, err := tb.Queries.ListRunEvents(ctx, pgID)
	if err != nil {
		return nil, err
	}
	if len(events) > args.Limit {
		events = events[len(events)-args.Limit:]
	}
	type out struct {
		Ts        string          `json:"ts"`
		Level     string          `json:"level"`
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"payload,omitempty"`
	}
	res := make([]out, len(events))
	for i, e := range events {
		o := out{
			Ts:        e.Ts.Time.UTC().Format("2006-01-02T15:04:05.000Z"),
			Level:     e.Level,
			EventType: e.EventType,
		}
		if len(e.PayloadJson) > 0 {
			o.Payload = json.RawMessage(e.PayloadJson)
		}
		res[i] = o
	}
	return json.Marshal(res)
}

func (tb Toolbox) listSkills(ctx context.Context) (json.RawMessage, error) {
	rows, err := tb.Queries.ListSkillsByOrg(ctx, tb.OrgID)
	if err != nil {
		return nil, err
	}
	type out struct {
		Path     string `json:"path"`
		Name     string `json:"name"`
		Owner    string `json:"owner"`
		RepoName string `json:"repo_name"`
	}
	res := make([]out, len(rows))
	for i, r := range rows {
		res[i] = out{Path: r.Path, Name: r.Name, Owner: r.Owner, RepoName: r.RepoName}
	}
	return json.Marshal(res)
}

func (tb Toolbox) listSecretNames(ctx context.Context) (json.RawMessage, error) {
	// We deliberately use the DB query rather than the SecretStore.List —
	// the latter is package-level on the secrets server, while the toolbox
	// wants to stay free of that dependency. Both surface only names.
	rows, err := tb.Queries.ListSecretNames(ctx, tb.OrgID)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	return json.Marshal(names)
}

func (tb Toolbox) listRepos(ctx context.Context) (json.RawMessage, error) {
	rows, err := tb.Queries.ListRepoConnections(ctx, tb.OrgID)
	if err != nil {
		return nil, err
	}
	type out struct {
		Owner         string `json:"owner"`
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
	}
	res := make([]out, len(rows))
	for i, r := range rows {
		res[i] = out{Owner: r.Owner, Name: r.Name, DefaultBranch: r.DefaultBranch}
	}
	return json.Marshal(res)
}

func (tb Toolbox) getAlerts(ctx context.Context) (json.RawMessage, error) {
	quiet, err := tb.Queries.ListSchedulesForQuietCheck(ctx, tb.OrgID)
	if err != nil {
		return nil, err
	}
	paused, err := tb.Queries.ListRecentAutoPaused(ctx, tb.OrgID)
	if err != nil {
		return nil, err
	}

	type quietRow struct {
		Name        string  `json:"name"`
		Cron        string  `json:"cron"`
		Timezone    string  `json:"timezone"`
		LastSuccess *string `json:"last_success"`
	}
	type pausedRow struct {
		Name        string  `json:"name"`
		PausedAt    *string `json:"paused_at"`
		PauseReason *string `json:"pause_reason"`
	}
	q := make([]quietRow, len(quiet))
	for i, r := range quiet {
		q[i] = quietRow{
			Name:        r.Name,
			Cron:        r.Cron,
			Timezone:    r.Timezone,
			LastSuccess: tsPtr(r.LastSuccess),
		}
	}
	p := make([]pausedRow, len(paused))
	for i, r := range paused {
		p[i] = pausedRow{
			Name:        r.Name,
			PausedAt:    tsPtr(r.AutoPausedAt),
			PauseReason: r.AutoPauseReason,
		}
	}
	return json.Marshal(map[string]any{
		"quiet_check_candidates": q,
		"recently_auto_paused":   p,
	})
}

// uuidStr renders a pgtype.UUID as a canonical hyphenated string. Returns
// the empty string for invalid UUIDs.
func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func tsPtr(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.UTC().Format("2006-01-02T15:04:05.000Z")
	return &s
}
