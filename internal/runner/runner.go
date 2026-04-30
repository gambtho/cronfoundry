// Package runner orchestrates a single end-to-end skill execution:
// load manifest + skill, call LLM, parse memory, publish, writeback.
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/mcp"
	"github.com/gambtho/cronfoundry/internal/memory"
	"github.com/gambtho/cronfoundry/internal/publish"
	runnersecrets "github.com/gambtho/cronfoundry/internal/secrets/runner"
	"github.com/gambtho/cronfoundry/internal/template"
	"github.com/gambtho/cronfoundry/internal/writeback"
)

const (
	DefaultMaxTurns            = 20
	DefaultMCPToolCallTimeoutS = 60
)

// Status is the terminal status of a Run.
type Status string

const (
	StatusSucceeded      Status = "succeeded"
	StatusPartialFailure Status = "partial_failure"
	StatusFailed         Status = "failed"
)

// mcpManager is the subset of mcp.Manager that the runner needs.
type mcpManager interface {
	Start(name, command string, args, env []string) error
	Tools(name string) []mcp.Tool
	DispatchAll(ctx context.Context, calls []mcp.ToolUse, perToolTimeout time.Duration) ([]mcp.CallResult, *mcp.FatalError)
	Shutdown()
}

// RunInput bundles all inputs to a single Run invocation.
type RunInput struct {
	RepoRoot     string
	ManifestPath string // relative to RepoRoot
	SkillPath    string // from manifest
	ScheduleName string

	RunID string

	Secrets *runnersecrets.Resolver

	LLMAPIKey     string
	LLMEndpoint   string // Azure AI Foundry only
	LLMDeployment string // Azure AI Foundry only

	DryRun   bool // don't publish, don't writeback, don't push
	SkipPush bool // writeback commit locally but don't push

	GitHubUsername string
	GitHubToken    string

	MCPServers []config.MCPServer
	MCPEnv     map[string]map[string]config.EnvValue
	MaxTurns   int
}

// RunResult is the terminal state of a single run.
type RunResult struct {
	Status         Status
	ErrorKind      string
	Usage          llm.Usage
	CostCents      int
	Output         string
	MemoryContent  string
	PublishResults []publish.Result
	WritebackSHA   string
	StartedAt      time.Time
	FinishedAt     time.Time
}

// EventType identifies a structured runner event.
type EventType string

const (
	EventMCPServerStartOK  EventType = "mcp.server.start.ok"
	EventMCPTurnStart      EventType = "mcp.turn.start"
	EventMCPToolCallOK     EventType = "mcp.tool.call.ok"
	EventMCPToolCallFail   EventType = "mcp.tool.call.fail"
	EventMCPToolCallTimeout EventType = "mcp.tool.call.timeout"
)

// RunEvent is a structured event the runner emits during execution.
type RunEvent struct {
	Type    EventType
	Payload map[string]any
}

// Deps inject the runner's collaborators.
type Deps struct {
	ProviderFactory   func(name string) (llm.Provider, error)
	Publishers        map[string]publish.Publisher
	MCPManagerFactory func(ctx context.Context) mcpManager
	Now               func() time.Time
	// EventSink receives observability events during execution. May be nil.
	EventSink func(RunEvent)
}

// Runner executes a single skill-schedule run.
type Runner struct {
	deps Deps
}

// New constructs a Runner.
func New(d Deps) *Runner {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ProviderFactory == nil {
		d.ProviderFactory = llm.NewProvider
	}
	if d.MCPManagerFactory == nil {
		d.MCPManagerFactory = func(ctx context.Context) mcpManager { return mcp.NewManager(ctx) }
	}
	if d.EventSink == nil {
		d.EventSink = func(RunEvent) {}
	}
	return &Runner{deps: d}
}

// Run executes a full skill invocation: load → LLM → parse → publish → writeback.
func (r *Runner) Run(ctx context.Context, in RunInput) (RunResult, error) {
	result := RunResult{StartedAt: r.deps.Now()}

	manifestBytes, err := os.ReadFile(filepath.Join(in.RepoRoot, in.ManifestPath))
	if err != nil {
		return fail(&result, fmt.Errorf("read manifest: %w", err), r.deps.Now)
	}
	m, err := config.ParseManifest(manifestBytes)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}
	if err := m.Validate(); err != nil {
		return fail(&result, err, r.deps.Now)
	}
	_, sch, err := m.FindSchedule(in.SkillPath, in.ScheduleName)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	skillMDPath := filepath.Join(in.RepoRoot, in.SkillPath, "SKILL.md")
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		return fail(&result, fmt.Errorf("read SKILL.md: %w", err), r.deps.Now)
	}
	skill, err := config.ParseSkillFile(skillBytes)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	skillRoot := filepath.Join(in.RepoRoot, in.SkillPath)
	body, err := config.ResolveIncludes(skill.Body, skillRoot)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	envBanner := buildEnvBanner(sch.Env)

	provider, err := r.deps.ProviderFactory(sch.Provider)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	var llmOutput string

	if len(in.MCPServers) == 0 {
		// Non-tool path: single-shot Chat call.
		var msgs []llm.Message
		if envBanner != "" {
			msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: envBanner})
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: body})

		var sb strings.Builder
		usage, err := provider.Chat(ctx, msgs, llm.CallOptions{
			Model:      sch.Model,
			MaxTokens:  skill.Frontmatter.MaxTokens,
			APIKey:     in.LLMAPIKey,
			Endpoint:   in.LLMEndpoint,
			Deployment: in.LLMDeployment,
		}, func(c llm.StreamChunk) { sb.WriteString(c.Delta) })
		if err != nil {
			return fail(&result, err, r.deps.Now)
		}
		result.Usage = usage
		llmOutput = sb.String()
	} else {
		// Tool-aware path: multi-turn loop via ChatTurn + MCP dispatch.
		toolProvider, ok := provider.(llm.ToolCapableProvider)
		if !ok {
			return failWithKind(&result, "provider_tool_unsupported",
				fmt.Errorf("provider %s does not support tool use", sch.Provider), r.deps.Now)
		}

		mgr := r.deps.MCPManagerFactory(ctx)
		defer mgr.Shutdown()

		for _, s := range in.MCPServers {
			env, err := resolveServerEnv(s.Name, in.MCPEnv, in.Secrets)
			if err != nil {
			return failWithKind(&result, "mcp_env_resolve", err, r.deps.Now)
			}
			if err := mgr.Start(s.Name, s.Command, s.Args, env); err != nil {
				return failWithKind(&result, "mcp_server_start_failed", err, r.deps.Now)
			}
			toolCount := len(mgr.Tools(s.Name))
			r.deps.EventSink(RunEvent{
				Type:    EventMCPServerStartOK,
				Payload: map[string]any{"server": s.Name, "tool_count": toolCount},
			})
		}

		var tools []llm.ToolDef
		for _, s := range in.MCPServers {
			for _, t := range mgr.Tools(s.Name) {
				tools = append(tools, llm.ToolDef{
					Name:        s.Name + "__" + t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
				})
			}
		}

		var messages []llm.Message
		if envBanner != "" {
			messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: envBanner})
		}
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: body})

		maxTurns := resolveMaxTurns(in.MaxTurns, skill.Frontmatter.MaxTurns)
		perToolTimeout := time.Duration(DefaultMCPToolCallTimeoutS) * time.Second

		terminated := false
		for turn := 1; turn <= maxTurns; turn++ {
			r.deps.EventSink(RunEvent{
				Type:    EventMCPTurnStart,
				Payload: map[string]any{"turn": turn},
			})
			tr, err := toolProvider.ChatTurn(ctx, messages, tools, llm.CallOptions{
				Model:      sch.Model,
				MaxTokens:  skill.Frontmatter.MaxTokens,
				APIKey:     in.LLMAPIKey,
				Endpoint:   in.LLMEndpoint,
				Deployment: in.LLMDeployment,
			}, func(llm.StreamChunk) {})
			if err != nil {
				return failWithKind(&result, "llm_error", err, r.deps.Now)
			}
			result.Usage.InputTokens += tr.Usage.InputTokens
			result.Usage.OutputTokens += tr.Usage.OutputTokens

			messages = append(messages, llm.Message{
				Role: llm.RoleAssistant, Content: tr.Text, ToolUses: tr.ToolUses,
			})

			if len(tr.ToolUses) == 0 {
				llmOutput = tr.Text
				terminated = true
				break
			}

			mcpCalls := toMCPCalls(tr.ToolUses)
			// Build id→name map for event attribution.
			callName := make(map[string]string, len(mcpCalls))
			for _, c := range mcpCalls {
				callName[c.ID] = c.Name
			}
			results, fatal := mgr.DispatchAll(ctx, mcpCalls, perToolTimeout)
			if fatal != nil {
				// Emit an event for every fatal kind so the UI log shows what happened
				// before the run terminates. Timeout and server crash are the two most
				// common; the event type carries the kind for forward-compat.
				r.deps.EventSink(RunEvent{
					Type:    "mcp.fatal." + EventType(fatal.Kind),
					Payload: map[string]any{"error": fatal.Err.Error()},
				})
				return failWithKind(&result, fatal.Kind, fatal.Err, r.deps.Now)
			}
			for _, cr := range results {
				evType := EventMCPToolCallOK
				if cr.IsError {
					evType = EventMCPToolCallFail
				}
				r.deps.EventSink(RunEvent{
					Type: evType,
					Payload: map[string]any{
						"tool":        callName[cr.ID],
						"duration_ms": cr.DurationMS,
					},
				})
				messages = append(messages, llm.Message{
					Role: llm.RoleTool, ToolUseID: cr.ID, Content: string(cr.ResultJSON),
				})
			}
		}

		if !terminated {
			return failWithKind(&result, "max_turns_exceeded",
				fmt.Errorf("exceeded %d turns without end_turn", maxTurns), r.deps.Now)
		}
	}

	result.CostCents = llm.CostCents(sch.Provider, sch.Model, result.Usage)

	published, memBlock, hasMemory := memory.Extract(llmOutput)
	result.Output = published
	result.MemoryContent = memBlock

	if in.DryRun {
		result.Status = StatusSucceeded
		result.FinishedAt = r.deps.Now()
		return result, nil
	}

	runID := in.RunID
	if runID == "" {
		runID = fmt.Sprintf("local-%d", result.StartedAt.UnixNano())
	}
	tctx := template.Context{
		Output:    published,
		RunID:     runID,
		RunDate:   result.StartedAt.Format("2006-01-02"),
		StartedAt: result.StartedAt,
		Schedule:  template.Meta{Name: sch.Name},
		Skill:     template.Meta{Name: skill.Frontmatter.Name},
	}
	outputBlocks, remaining := memory.ExtractOutputBlocks(published)
	dispatcher := &publish.Dispatcher{Publishers: r.deps.Publishers}
	pubResults := dispatcher.Dispatch(ctx, sch.Destinations, func(dest config.Destination) string {
		outputName := ""
		switch {
		case dest.Slack != nil:
			outputName = dest.Slack.Output
		case dest.Discord != nil:
			outputName = dest.Discord.Output
		case dest.Teams != nil:
			outputName = dest.Teams.Output
		case dest.GitHubIssue != nil:
			outputName = dest.GitHubIssue.Output
		}
		return selectOutputForDest(outputName, outputBlocks, remaining)
	}, tctx, in.Secrets, true)
	result.PublishResults = pubResults

	allOK := true
	for _, pr := range pubResults {
		if !pr.OK && !pr.Skipped {
			allOK = false
		}
	}

	writebackOK := true
	if sch.Writeback != nil && sch.Writeback.Enabled && hasMemory {
		msg := fmt.Sprintf("chore(cronfoundry): update %s", sch.Writeback.Path)
		if in.RunID != "" {
			msg = fmt.Sprintf("chore(cronfoundry): update %s from run %s", sch.Writeback.Path, in.RunID)
		}
		sha, err := writeback.New().Commit(in.RepoRoot, writeback.Options{
			Path:        sch.Writeback.Path,
			Mode:        sch.Writeback.Mode,
			Content:     memBlock,
			Message:     msg,
			AuthorName:  "cronfoundry[bot]",
			AuthorEmail: "cronfoundry[bot]@users.noreply.github.com",
		})
		if err != nil {
			writebackOK = false
			slog.Error("writeback commit failed", "err", err, "path", sch.Writeback.Path, "run_id", runID)
		} else {
			result.WritebackSHA = sha
			switch {
			case in.SkipPush:
			case in.GitHubToken == "":
				slog.Warn("writeback committed locally but not pushed (no GitHub token)",
					"commit", sha, "path", sch.Writeback.Path)
			default:
				if err := writeback.New().Push(in.RepoRoot, "origin", in.GitHubUsername, in.GitHubToken); err != nil {
					writebackOK = false
					slog.Error("writeback push failed", "err", err, "path", sch.Writeback.Path, "run_id", runID, "sha", sha)
				}
			}
		}
	}

	switch {
	case allOK && writebackOK:
		result.Status = StatusSucceeded
	default:
		result.Status = StatusPartialFailure
	}
	result.FinishedAt = r.deps.Now()
	return result, nil
}

func buildEnvBanner(env map[string]config.EnvValue) string {
	if len(env) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<env>\n")
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		v := env[k]
		if v.Secret != "" {
			fmt.Fprintf(&b, "%s=[secret]\n", k)
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v.Literal)
	}
	b.WriteString("</env>")
	return b.String()
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

func fail(r *RunResult, err error, now func() time.Time) (RunResult, error) {
	r.Status = StatusFailed
	r.FinishedAt = now()
	return *r, err
}

func failWithKind(r *RunResult, kind string, err error, now func() time.Time) (RunResult, error) {
	r.ErrorKind = kind
	return fail(r, err, now)
}

func resolveMaxTurns(fromSchedule, fromSkill int) int {
	if fromSchedule > 0 {
		return fromSchedule
	}
	if fromSkill > 0 {
		return fromSkill
	}
	return DefaultMaxTurns
}

func resolveServerEnv(name string, mcpEnv map[string]map[string]config.EnvValue, s *runnersecrets.Resolver) ([]string, error) {
	if mcpEnv == nil {
		return nil, nil
	}
	var env []string
	for k, v := range mcpEnv[name] {
		if v.Secret != "" && s != nil {
			val, err := s.Get(v.Secret)
			if err != nil {
			return nil, fmt.Errorf("mcp server %s env %s: %w", name, k, err)
			}
			env = append(env, k+"="+val)
		} else {
			env = append(env, k+"="+v.Literal)
		}
	}
	return env, nil
}

func toMCPCalls(in []llm.ToolUse) []mcp.ToolUse {
	out := make([]mcp.ToolUse, len(in))
	for i, t := range in {
		out[i] = mcp.ToolUse{ID: t.ID, Name: t.Name, Input: t.Input}
	}
	return out
}

// selectOutputForDest returns the named output block if name is non-empty and
// present in blocks; otherwise returns fallback.
func selectOutputForDest(name string, blocks map[string]string, fallback string) string {
	if name == "" {
		return fallback
	}
	if v, ok := blocks[name]; ok {
		return v
	}
	return fallback
}
