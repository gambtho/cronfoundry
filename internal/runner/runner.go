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
	"github.com/gambtho/cronfoundry/internal/memory"
	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/secrets"
	"github.com/gambtho/cronfoundry/internal/template"
	"github.com/gambtho/cronfoundry/internal/writeback"
)

// Status is the terminal status of a Run.
type Status string

const (
	StatusSucceeded      Status = "succeeded"
	StatusPartialFailure Status = "partial_failure"
	StatusFailed         Status = "failed"
)

// RunInput bundles all inputs to a single Run invocation.
type RunInput struct {
	RepoRoot     string
	ManifestPath string // relative to RepoRoot
	SkillPath    string // from manifest
	ScheduleName string

	Secrets *secrets.Resolver

	LLMAPIKey     string
	LLMEndpoint   string // overrides provider's default API base URL; required for azure-foundry
	LLMDeployment string // Azure AI Foundry only

	DryRun   bool // don't publish, don't writeback, don't push
	SkipPush bool // writeback commit locally but don't push

	GitHubUsername string
	GitHubToken    string
}

// RunResult is the terminal state of a single run.
type RunResult struct {
	Status         Status
	Usage          llm.Usage
	Output         string
	MemoryContent  string
	PublishResults []publish.Result
	WritebackSHA   string
	StartedAt      time.Time
	FinishedAt     time.Time
}

// Deps inject the runner's collaborators. Tests pass fakes; the CLI wires real
// provider factory and publishers.
type Deps struct {
	ProviderFactory func(name, endpoint string) (llm.Provider, error)
	Publishers      map[string]publish.Publisher
	Now             func() time.Time
}

// Runner executes a single skill-schedule run.
type Runner struct {
	deps Deps
}

// New constructs a Runner. Missing Deps fields are defaulted (Now=time.Now,
// ProviderFactory=llm.NewProvider).
func New(d Deps) *Runner {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ProviderFactory == nil {
		d.ProviderFactory = llm.NewProvider
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

	// Load SKILL.md.
	skillMDPath := filepath.Join(in.RepoRoot, in.SkillPath, "SKILL.md")
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		return fail(&result, fmt.Errorf("read SKILL.md: %w", err), r.deps.Now)
	}
	skill, err := config.ParseSkillFile(skillBytes)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Resolve includes against the skill's directory.
	skillRoot := filepath.Join(in.RepoRoot, in.SkillPath)
	body, err := config.ResolveIncludes(skill.Body, skillRoot)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Build env banner.
	envBanner, err := buildEnvBanner(sch.Env, in.Secrets)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	// Compose messages: system = env banner, user = skill body.
	var msgs []llm.Message
	if envBanner != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: envBanner})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: body})

	provider, err := r.deps.ProviderFactory(sch.Provider, in.LLMEndpoint)
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}

	var sb strings.Builder
	usage, err := provider.Chat(ctx, msgs, llm.CallOptions{
		Model:      sch.Model,
		MaxTokens:  skill.Frontmatter.MaxTokens,
		APIKey:     in.LLMAPIKey,
		Deployment: in.LLMDeployment,
	}, func(c llm.StreamChunk) { sb.WriteString(c.Delta) })
	if err != nil {
		return fail(&result, err, r.deps.Now)
	}
	result.Usage = usage

	published, memBlock, hasMemory := memory.Extract(sb.String())
	result.Output = published
	result.MemoryContent = memBlock

	if in.DryRun {
		result.Status = StatusSucceeded
		result.FinishedAt = r.deps.Now()
		return result, nil
	}

	tctx := template.Context{
		Output:    published,
		RunID:     fmt.Sprintf("local-%d", result.StartedAt.UnixNano()),
		RunDate:   result.StartedAt.Format("2006-01-02"),
		StartedAt: result.StartedAt,
		Schedule:  template.Meta{Name: sch.Name},
		Skill:     template.Meta{Name: skill.Frontmatter.Name},
	}
	dispatcher := &publish.Dispatcher{Publishers: r.deps.Publishers}
	pubResults := dispatcher.Dispatch(ctx, sch.Destinations, published, tctx, in.Secrets)
	result.PublishResults = pubResults

	allOK := true
	for _, pr := range pubResults {
		if !pr.OK {
			allOK = false
		}
	}

	writebackOK := true
	if sch.Writeback != nil && sch.Writeback.Enabled && hasMemory {
		sha, err := writeback.New().Commit(in.RepoRoot, writeback.Options{
			Path:        sch.Writeback.Path,
			Mode:        sch.Writeback.Mode,
			Content:     memBlock,
			Message:     fmt.Sprintf("chore(cronfoundry): update %s", sch.Writeback.Path),
			AuthorName:  "cronfoundry[bot]",
			AuthorEmail: "cronfoundry[bot]@users.noreply.github.com",
		})
		if err != nil {
			writebackOK = false
		} else {
			result.WritebackSHA = sha
			switch {
			case in.SkipPush:
				// explicit opt-out; no log
			case in.GitHubToken == "":
				// Writeback succeeded locally but we have no credentials
				// to push. Warn the user so they don't silently accrue
				// local-only commits when they expected remote updates.
				slog.Warn("writeback committed locally but not pushed (no GitHub token)",
					"commit", sha, "path", sch.Writeback.Path)
			default:
				if err := writeback.New().Push(in.RepoRoot, "origin", in.GitHubUsername, in.GitHubToken); err != nil {
					writebackOK = false
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

// buildEnvBanner composes a <env>KEY=VALUE\n...</env> block for the system
// message. Keys are sorted for deterministic output.
func buildEnvBanner(env map[string]config.EnvValue, s *secrets.Resolver) (string, error) {
	if len(env) == 0 {
		return "", nil
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
		val := v.Literal
		if v.Secret != "" {
			resolved, err := s.Get(v.Secret)
			if err != nil {
				return "", fmt.Errorf("env %s: %w", k, err)
			}
			val = resolved
		}
		fmt.Fprintf(&b, "%s=%s\n", k, val)
	}
	b.WriteString("</env>")
	return b.String(), nil
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
