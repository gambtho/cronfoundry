// Package main is the CronFoundry runner CLI: executes a single skill-
// schedule invocation end-to-end (load manifest → LLM → publish → writeback).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/redact"
	"github.com/gambtho/cronfoundry/internal/runner"
	"github.com/gambtho/cronfoundry/internal/secrets"
)

func main() {
	var (
		repoRoot     string
		manifestPath string
		skillPath    string
		scheduleName string
		llmKeyEnv    string
		llmEndpoint  string
		llmDeploy    string
		dryRun       bool
		skipPush     bool
	)

	root := &cobra.Command{
		Use:           "cronfoundry-runner",
		Short:         "Execute a CronFoundry skill once",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run a single schedule from a cronfoundry.yaml manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			env := envAsMap()
			sec := secrets.New(env)

			// Build redactor including all known secret values + LLM key + GH token.
			redactValues := sec.AllValues()
			if k, ok := env[llmKeyEnv]; ok {
				redactValues = append(redactValues, k)
			}
			if tok, ok := env["GITHUB_TOKEN"]; ok {
				redactValues = append(redactValues, tok)
			}
			redactor := redact.New(redactValues)

			logger := slog.New(redactingHandler{inner: slog.NewTextHandler(os.Stderr, nil), r: redactor})
			slog.SetDefault(logger)

			r := runner.New(runner.Deps{
				Publishers: map[string]publish.Publisher{
					"github-issue": publish.NewGitHubIssuePublisher("", env["GITHUB_TOKEN"]),
					"slack":        publish.NewSlackPublisher(),
					"discord":      publish.NewDiscordPublisher(),
					"teams":        publish.NewTeamsPublisher(),
				},
			})

			llmKey := env[llmKeyEnv]
			if llmKey == "" {
				return fmt.Errorf("LLM key env var %q is empty", llmKeyEnv)
			}

			result, err := r.Run(ctx, runner.RunInput{
				RepoRoot:       repoRoot,
				ManifestPath:   manifestPath,
				SkillPath:      skillPath,
				ScheduleName:   scheduleName,
				Secrets:        sec,
				LLMAPIKey:      llmKey,
				LLMEndpoint:    llmEndpoint,
				LLMDeployment:  llmDeploy,
				DryRun:         dryRun,
				SkipPush:       skipPush,
				GitHubUsername: "cronfoundry-bot",
				GitHubToken:    env["GITHUB_TOKEN"],
			})
			if err != nil {
				slog.Error("run failed", "err", err)
				return err
			}

			summary := map[string]any{
				"status":          string(result.Status),
				"input_tokens":    result.Usage.InputTokens,
				"output_tokens":   result.Usage.OutputTokens,
				"started_at":      result.StartedAt,
				"finished_at":     result.FinishedAt,
				"writeback_sha":   result.WritebackSHA,
				"publish_results": pubSummary(result.PublishResults),
			}
			b, _ := json.MarshalIndent(summary, "", "  ")
			fmt.Println(string(b))

			switch result.Status {
			case runner.StatusSucceeded:
				return nil
			default:
				return fmt.Errorf("run finished with status %s", result.Status)
			}
		},
	}

	flags := runCmd.Flags()
	flags.StringVar(&repoRoot, "repo", ".", "path to the skill repo root")
	flags.StringVar(&manifestPath, "manifest", "cronfoundry.yaml", "path to manifest, relative to --repo")
	flags.StringVar(&skillPath, "skill-path", "", "skill path as declared in the manifest (required)")
	flags.StringVar(&scheduleName, "schedule-name", "", "schedule name within the skill (required)")
	flags.StringVar(&llmKeyEnv, "llm-key-env", "OPENAI_API_KEY", "env var name that holds the LLM API key")
	flags.StringVar(&llmEndpoint, "llm-endpoint", "", "Azure AI Foundry endpoint (azure-foundry provider only)")
	flags.StringVar(&llmDeploy, "llm-deployment", "", "Azure AI Foundry deployment name")
	flags.BoolVar(&dryRun, "dry-run", false, "skip publish and writeback; print output only")
	flags.BoolVar(&skipPush, "skip-push", false, "perform writeback commit locally but do not push")
	_ = runCmd.MarkFlagRequired("skill-path")
	_ = runCmd.MarkFlagRequired("schedule-name")

	root.AddCommand(runCmd)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// redactingHandler wraps a slog.Handler and scrubs known secret values from
// message text and string-valued attributes before delegating to the inner
// handler.
type redactingHandler struct {
	inner slog.Handler
	r     *redact.Redactor
}

func (h redactingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}
func (h redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	rec.Message = h.r.Redact(rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		if a.Value.Kind() == slog.KindString {
			a.Value = slog.StringValue(h.r.Redact(a.Value.String()))
		}
		return true
	})
	return h.inner.Handle(ctx, rec)
}
func (h redactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return redactingHandler{inner: h.inner.WithAttrs(as), r: h.r}
}
func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{inner: h.inner.WithGroup(name), r: h.r}
}

func envAsMap() map[string]string {
	env := os.Environ()
	out := make(map[string]string, len(env))
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out
}

func pubSummary(rs []publish.Result) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		errStr := ""
		if r.Err != nil {
			errStr = r.Err.Error()
		}
		out = append(out, map[string]any{
			"type":   r.Type,
			"ok":     r.OK,
			"detail": r.Detail,
			"error":  errStr,
		})
	}
	return out
}
