// HTTP-mode runner subcommand. Unlike the standalone cronfoundry-runner in
// cmd/runner (which takes manifest+skill paths on the CLI and reads secrets
// from process env), this subcommand is invoked by the scheduler's
// SubprocessDispatcher: it reads run_id + token + API URL from env vars,
// fetches everything it needs over HTTP, then invokes the P1 runner library
// against a fresh clone.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/publish"
	"github.com/gambtho/cronfoundry/internal/runner"
	"github.com/gambtho/cronfoundry/internal/secrets"
)

const (
	envAPIURL   = "CRONFOUNDRY_API_URL"
	envRunID    = "CRONFOUNDRY_RUN_ID"
	envRunToken = "CRONFOUNDRY_RUN_TOKEN"
)

// newRunnerCmd registers the HTTP-mode runner subcommand on the cronfoundry
// root command. It is marked hidden because only the scheduler should invoke
// it directly; operators use the standalone cronfoundry-runner binary for
// local execution.
func newRunnerCmd() *cobra.Command {
	var runIDFlag string
	cmd := &cobra.Command{
		Use:    "runner",
		Short:  "Execute a single run in HTTP mode (fetches context from API)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunnerHTTP(cmd.Context(), runIDFlag)
		},
	}
	cmd.Flags().StringVar(&runIDFlag, "run-id", "", "run UUID (also read from CRONFOUNDRY_RUN_ID)")
	return cmd
}

// runRunnerHTTP is the HTTP-mode runner entry point. It's invoked by the
// scheduler via SubprocessDispatcher with env vars set; the --run-id flag is
// a convenience override for manual invocation during development.
func runRunnerHTTP(ctx context.Context, runIDFlag string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	apiURL := os.Getenv(envAPIURL)
	if apiURL == "" {
		return fmt.Errorf("%s is required", envAPIURL)
	}
	runID := runIDFlag
	if runID == "" {
		runID = os.Getenv(envRunID)
	}
	if runID == "" {
		return fmt.Errorf("%s or --run-id is required", envRunID)
	}
	runToken := os.Getenv(envRunToken)
	if runToken == "" {
		return fmt.Errorf("%s is required", envRunToken)
	}

	client := &apiClient{
		baseURL: strings.TrimRight(apiURL, "/"),
		token:   runToken,
		http:    &http.Client{Timeout: 2 * time.Minute},
	}

	// 1) Fetch the run's execution context.
	runCtx, err := client.GetRunContext(ctx, runID)
	if err != nil {
		return failRun(ctx, client, runID, "context_fetch", err)
	}

	// 2) Fetch scoped secrets. We collect every `{"secret": "name"}`
	//    reference from destinations + env, plus llm_secret_ref.
	secretNames := collectSecretNames(runCtx)
	secretMap, err := client.GetSecrets(ctx, secretNames)
	if err != nil {
		return failRun(ctx, client, runID, "secrets_fetch", err)
	}

	// 3) Fetch the tokenized clone URL.
	cloneURL, err := client.GetCloneURL(ctx, runCtx.RepoID)
	if err != nil {
		return failRun(ctx, client, runID, "clone_url_fetch", err)
	}

	// 4) Shallow-clone the skill repo at the pinned SHA.
	cloneDir, err := os.MkdirTemp("", "cronfoundry-run-*")
	if err != nil {
		return failRun(ctx, client, runID, "tempdir", err)
	}
	defer func() { _ = os.RemoveAll(cloneDir) }()

	// The clone URL already embeds the installation token as basic auth, so
	// we pass "" for the installToken arg of CloneAtSHA.
	//
	// A clone failure's error string may contain the full URL (and thus the
	// installation token) — redact before posting events or logging.
	if err := github.CloneAtSHA(ctx, cloneURL, "", runCtx.SkillSha, cloneDir); err != nil {
		return failRun(ctx, client, runID, "clone",
			fmt.Errorf("clone %s: %s", redactCloneURL(cloneURL), err.Error()))
	}

	// 5) Build a Resolver from the fetched secrets. P1's secrets.Resolver
	//    expects env-var-style keys (CRONFOUNDRY_SECRET_<UPPER(name)>) so we
	//    remap here.
	resolver := secrets.New(envMapForSecrets(secretMap))

	// 6) Extract the LLM API key. The provider factory needs it inline.
	llmAPIKey := ""
	if runCtx.LLMSecretRef != nil && *runCtx.LLMSecretRef != "" {
		if v, ok := secretMap[*runCtx.LLMSecretRef]; ok {
			llmAPIKey = v
		}
	}
	llmEndpoint := ""
	if runCtx.LLMEndpoint != nil {
		llmEndpoint = *runCtx.LLMEndpoint
	}
	llmDeployment := ""
	if runCtx.LLMDeployment != nil {
		llmDeployment = *runCtx.LLMDeployment
	}

	// 7) Announce start. Best-effort — if the event POST fails we log-and-
	//    continue rather than abort the run.
	startedAt := time.Now()
	_ = client.PostEvents(ctx, runID, []event{{
		Type:    "runner.start",
		Level:   "info",
		Payload: map[string]any{"started_at": startedAt.UTC().Format(time.RFC3339)},
	}})

	// 8) Invoke the P1 runner. Note: runner.Run is a METHOD on *Runner, not
	//    a free function — we construct via runner.New(Deps{...}).
	r := runner.New(runner.Deps{
		Publishers: map[string]publish.Publisher{
			"github-issue": publish.NewGitHubIssuePublisher("", ""),
			"slack":        publish.NewSlackPublisher(),
			"discord":      publish.NewDiscordPublisher(),
			"teams":        publish.NewTeamsPublisher(),
		},
	})
	result, runErr := r.Run(ctx, runner.RunInput{
		RepoRoot:      cloneDir,
		ManifestPath:  "cronfoundry.yaml",
		SkillPath:     runCtx.SkillPath,
		ScheduleName:  runCtx.ScheduleName,
		Secrets:       resolver,
		LLMAPIKey:     llmAPIKey,
		LLMEndpoint:   llmEndpoint,
		LLMDeployment: llmDeployment,
		// Writeback push requires the installation token; the clone URL
		// embeds one but runner.Run expects GitHubToken separate. For now,
		// skip the push — P2d will wire a dedicated writeback token fetch.
		SkipPush: true,
	})

	// 9) Post a terminal event for observability.
	endType := "runner.finish"
	if runErr != nil {
		endType = "runner.error"
	}
	_ = client.PostEvents(ctx, runID, []event{{
		Type:  endType,
		Level: map[bool]string{true: "error", false: "info"}[runErr != nil],
		Payload: map[string]any{
			"status":        string(result.Status),
			"finished_at":   time.Now().UTC().Format(time.RFC3339),
			"input_tokens":  result.Usage.InputTokens,
			"output_tokens": result.Usage.OutputTokens,
		},
	}})

	// 10) Finalize.
	body := finalizeRequest{Status: string(result.Status)}
	if runErr != nil {
		body.Status = "failed"
		msg := runErr.Error()
		body.ErrorMsg = &msg
		kind := "runner"
		body.ErrorKind = &kind
	}
	if !result.StartedAt.IsZero() && !result.FinishedAt.IsZero() {
		d := int32(result.FinishedAt.Sub(result.StartedAt) / time.Millisecond)
		body.DurationMs = &d
	}
	if result.Usage.InputTokens > 0 {
		v := int32(result.Usage.InputTokens)
		body.TokensIn = &v
	}
	if result.Usage.OutputTokens > 0 {
		v := int32(result.Usage.OutputTokens)
		body.TokensOut = &v
	}
	if result.WritebackSHA != "" {
		sha := result.WritebackSHA
		body.WritebackCommitSha = &sha
	}
	if err := client.PostFinalize(ctx, runID, body); err != nil {
		return fmt.Errorf("finalize: %w", err)
	}

	if runErr != nil {
		return fmt.Errorf("runner: %w", runErr)
	}
	return nil
}

// redactCloneURL strips the userinfo component (which holds the installation
// token) from an otherwise-opaque clone URL, so the URL is safe to embed in
// error messages and event payloads. A URL we can't parse collapses to a
// constant rather than being returned verbatim, since a parse failure is
// often a sign that the string contains bytes we shouldn't log anyway.
func redactCloneURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "<clone-url>"
	}
	u.User = nil
	return u.String()
}

// envMapForSecrets converts a name → value map into CRONFOUNDRY_SECRET_<UPPER>
// keys that P1's secrets.Resolver expects (see internal/secrets/resolver.go).
func envMapForSecrets(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for name, val := range m {
		out["CRONFOUNDRY_SECRET_"+strings.ToUpper(name)] = val
	}
	return out
}

// failRun posts a finalize-failed with the given kind + error message and
// returns a wrapped error. Best-effort — if the finalize POST itself fails,
// we still return the original error so the caller sees what went wrong.
func failRun(ctx context.Context, client *apiClient, runID, kind string, err error) error {
	errMsg := err.Error()
	_ = client.PostFinalize(ctx, runID, finalizeRequest{
		Status:    "failed",
		ErrorKind: &kind,
		ErrorMsg:  &errMsg,
	})
	return fmt.Errorf("runner %s: %w", kind, err)
}

// ---------- API client ----------

// apiClient is the runner's thin client for the /internal/* surface. It holds
// a bearer token minted by the scheduler and scoped to exactly one run.
type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// runContext mirrors api.RunContext. Duplicated here so the runner doesn't
// pull in the api package (which transitively pulls in pgx and server
// plumbing).
type runContext struct {
	RunID          string          `json:"run_id"`
	OrgID          string          `json:"org_id"`
	ScheduleName   string          `json:"schedule_name"`
	SkillPath      string          `json:"skill_path"`
	SkillSha       string          `json:"skill_sha"`
	Repo           string          `json:"repo"`
	RepoID         string          `json:"repo_id"`
	DefaultBranch  string          `json:"default_branch"`
	InstallationID int64           `json:"installation_id"`
	Provider       string          `json:"provider"`
	Model          string          `json:"model"`
	LLMSecretRef   *string         `json:"llm_secret_ref,omitempty"`
	LLMEndpoint    *string         `json:"llm_endpoint,omitempty"`
	LLMDeployment  *string         `json:"llm_deployment,omitempty"`
	Destinations   json.RawMessage `json:"destinations"`
	Writeback      json.RawMessage `json:"writeback,omitempty"`
	Env            json.RawMessage `json:"env"`
	Frontmatter    json.RawMessage `json:"frontmatter"`
}

func (c *apiClient) GetRunContext(ctx context.Context, runID string) (runContext, error) {
	var out runContext
	err := c.do(ctx, http.MethodGet, "/internal/runs/"+url.PathEscape(runID)+"/context", nil, &out)
	return out, err
}

func (c *apiClient) GetSecrets(ctx context.Context, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return map[string]string{}, nil
	}
	q := url.Values{}
	q.Set("names", strings.Join(names, ","))
	var out map[string]string
	err := c.do(ctx, http.MethodGet, "/internal/secrets?"+q.Encode(), nil, &out)
	return out, err
}

func (c *apiClient) GetCloneURL(ctx context.Context, repoID string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	err := c.do(ctx, http.MethodGet, "/internal/repos/"+url.PathEscape(repoID)+"/clone-url", nil, &out)
	return out.URL, err
}

// event mirrors the shape of the POST /internal/runs/{id}/events body
// elements — type, level, payload.
type event struct {
	Type    string         `json:"type"`
	Level   string         `json:"level"`
	Payload map[string]any `json:"payload,omitempty"`
}

func (c *apiClient) PostEvents(ctx context.Context, runID string, events []event) error {
	body := map[string]any{"events": events}
	return c.do(ctx, http.MethodPost, "/internal/runs/"+url.PathEscape(runID)+"/events", body, nil)
}

// finalizeRequest mirrors api.finalizeBody (unexported there; we redeclare it
// rather than importing, to keep this package decoupled from the server).
type finalizeRequest struct {
	Status             string  `json:"status"`
	DurationMs         *int32  `json:"duration_ms,omitempty"`
	TokensIn           *int32  `json:"tokens_in,omitempty"`
	TokensOut          *int32  `json:"tokens_out,omitempty"`
	CostCents          *int32  `json:"cost_cents,omitempty"`
	ErrorKind          *string `json:"error_kind,omitempty"`
	ErrorMsg           *string `json:"error_msg,omitempty"`
	WritebackCommitSha *string `json:"writeback_commit_sha,omitempty"`
}

func (c *apiClient) PostFinalize(ctx context.Context, runID string, body finalizeRequest) error {
	return c.do(ctx, http.MethodPost, "/internal/runs/"+url.PathEscape(runID)+"/finalize", body, nil)
}

// do issues a JSON-over-HTTP request with bearer auth and decodes the
// response body into decodeInto (skipped if nil). Non-2xx responses become
// errors carrying the response body for debuggability.
func (c *apiClient) do(ctx context.Context, method, path string, body, decodeInto any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: http %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	if decodeInto != nil {
		if err := json.NewDecoder(resp.Body).Decode(decodeInto); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// collectSecretNames extracts the list of secret names referenced by the run.
//
// Rather than reparsing the full JSON schema for destinations + env — which
// evolves — we do a string scan for `"secret": "NAME"` pairs. This is robust
// against top-level array vs object shapes and against nested nesting in
// different publisher configs, at the cost of being slightly loose: literal
// values that happen to contain the string `"secret"` could theoretically
// produce a false match. In practice the JSON comes from our own manifest
// parser which only emits this pattern for actual secret references.
func collectSecretNames(ctx runContext) []string {
	seen := map[string]struct{}{}
	scan := func(raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		s := string(raw)
		i := 0
		for {
			idx := strings.Index(s[i:], `"secret"`)
			if idx < 0 {
				break
			}
			idx += i
			j := idx + len(`"secret"`)
			for j < len(s) && (s[j] == ' ' || s[j] == ':' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j >= len(s) || s[j] != '"' {
				i = idx + 1
				continue
			}
			j++
			end := strings.IndexByte(s[j:], '"')
			if end < 0 {
				break
			}
			end += j
			name := s[j:end]
			if name != "" {
				seen[name] = struct{}{}
			}
			i = end + 1
		}
	}
	scan(ctx.Destinations)
	scan(ctx.Env)
	if ctx.LLMSecretRef != nil && *ctx.LLMSecretRef != "" {
		seen[*ctx.LLMSecretRef] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}
