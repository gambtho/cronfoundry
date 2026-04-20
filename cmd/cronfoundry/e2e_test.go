//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/testdb"
)

const e2eAddr = "127.0.0.1:18090"

// TestE2E_FullScheduleFire starts a real serve binary, triggers a manual
// run, and watches it finalize successfully against faked GitHub + LLM.
func TestE2E_FullScheduleFire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in -short mode")
	}

	dsn, teardown := testdb.BootPGWithDSN(t)
	defer teardown()

	// Fixture skill repo on disk — used as the clone target.
	fixtureDir := buildFixtureRepo(t)
	headSHA := gitHeadSHA(t, fixtureDir)

	// Fake Discord webhook — records received payloads.
	var discordPayload []byte
	discordSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discordPayload, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer discordSrv.Close()

	// Fake OpenAI — returns a canned streaming completion.
	llmSrv := fakeOpenAI(t)
	defer llmSrv.Close()

	// Fake GitHub — serves install tokens + branch head.
	ghSrv := fakeGitHubAPI(t, headSHA)
	defer ghSrv.Close()

	// Build the binary.
	binPath := buildBinary(t)

	// Write a test GitHub App PEM.
	pemBytes, _ := githubtest.MustPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, pemBytes, 0o600))

	baseEnv := map[string]string{
		"CRONFOUNDRY_DATABASE_URL":      dsn,
		"CRONFOUNDRY_GITHUB_APP_ID":     "1",
		"CRONFOUNDRY_GITHUB_APP_PEM":    pemPath,
		"CRONFOUNDRY_GITHUB_BASE_URL":   ghSrv.URL,
		"CRONFOUNDRY_GITHUB_CLONE_BASE": filepath.Dir(filepath.Dir(fixtureDir)),
		"PATH":                          os.Getenv("PATH"),
	}

	// Step 1: admin init (no master key) — generates + prints the key.
	masterKey := adminInitGetKey(t, binPath, baseEnv)
	baseEnv["CRONFOUNDRY_MASTER_KEY"] = masterKey

	// Step 2: admin init (with key) — runs migrations, seeds org.
	runAdminCmd(t, binPath, baseEnv, nil, "admin", "init")

	// Step 3: connect repo.
	runAdminCmd(t, binPath, baseEnv, nil, "admin", "connect-repo", "fake/repo",
		"--installation-id", "1", "--branch", "main")

	// Step 4: set secrets.
	runAdminCmd(t, binPath, baseEnv, strings.NewReader(discordSrv.URL+"\n"),
		"admin", "set-secret", "discord_webhook")
	runAdminCmd(t, binPath, baseEnv, strings.NewReader("sk-fake\n"),
		"admin", "set-secret", "openai_key")

	// Step 5: trigger-sync (uses fake GitHub to fetch branch head + fixture clone).
	runAdminCmd(t, binPath, baseEnv, nil, "admin", "trigger-sync", "fake/repo")

	// Step 6: patch llm_endpoint + llm_secret_ref on the schedule row.
	// llm_secret_ref is not populated by trigger-sync (P3 sets it via UI);
	// here we wire it to the "openai_key" secret we set in Step 4, and point
	// llm_endpoint at the fake OpenAI server.
	{
		pool, err := pgxpool.New(context.Background(), dsn)
		require.NoError(t, err)
		defer pool.Close()
		secretRef := "openai_key"
		endpoint := llmSrv.URL
		_, err = pool.Exec(context.Background(),
			`UPDATE schedule SET llm_endpoint = $1, llm_secret_ref = $2`, endpoint, secretRef)
		require.NoError(t, err, "patch llm_endpoint + llm_secret_ref")
	}

	// Step 7: start serve in background.
	serveCtx, serveCancel := context.WithCancel(context.Background())
	defer serveCancel()
	serveEnv := copyEnv(baseEnv)
	serveCmd := exec.CommandContext(serveCtx, binPath,
		"serve", "--addr", e2eAddr, "--tick-cadence", "1s")
	serveCmd.Env = envSlice(serveEnv)
	serveLogs := &bytes.Buffer{}
	serveCmd.Stdout = serveLogs
	serveCmd.Stderr = serveLogs
	require.NoError(t, serveCmd.Start())
	defer func() { _ = serveCmd.Process.Kill() }()

	// Wait for healthz.
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + e2eAddr + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 200*time.Millisecond, "serve did not start")

	// Step 8: look up schedule ID and trigger a manual run.
	scheduleID := lookupScheduleID(t, dsn)
	runID := triggerRunNow(t, "http://"+e2eAddr, scheduleID)

	// Step 9: poll for run to finalize.
	var finalStatus string
	require.Eventually(t, func() bool {
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return false
		}
		defer pool.Close()
		var status string
		err = pool.QueryRow(context.Background(),
			`SELECT status FROM run WHERE id = $1`, runID).Scan(&status)
		if err == nil && status != "pending" && status != "running" {
			finalStatus = status
			return true
		}
		return false
	}, 60*time.Second, 500*time.Millisecond,
		"run did not finalize; serve logs:\n%s", serveLogs.String())

	t.Logf("run %s finalized with status=%s", runID, finalStatus)
	assert.NotEqual(t, "failed", finalStatus,
		"run failed; serve logs:\n%s", serveLogs.String())

	// Step 10: assert Discord received a payload.
	assert.NotEmpty(t, discordPayload, "Discord webhook was never called")
	var discordBody map[string]any
	require.NoError(t, json.Unmarshal(discordPayload, &discordBody))
	assert.NotEmpty(t, discordBody["content"], "Discord payload missing 'content'")
}

// ---------- helpers ----------

// buildFixtureRepo creates a minimal git repo with cronfoundry.yaml + SKILL.md
// and returns its path.
func buildFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "fake", "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	manifest := `version: 1
skills:
  - path: skills/hello
    schedules:
      - name: default
        cron: "*/5 * * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - discord:
              secret: discord_webhook
`
	skillMD := `---
name: hello
description: Test skill for e2e
---
Say "Hello from CronFoundry!" in a single Discord message.
`
	skillDir := filepath.Join(repoDir, "skills", "hello")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "cronfoundry.yaml"), []byte(manifest), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644))

	// Init a real git repo so CloneAtSHA can checkout a SHA.
	repo, err := gogit.PlainInit(repoDir, false)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin", URLs: []string{"https://github.com/fake/repo.git"},
	})
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add(".")
	require.NoError(t, err)
	_, err = wt.Commit("init", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@e2e.local", When: time.Now()},
	})
	require.NoError(t, err)

	return repoDir
}

func gitHeadSHA(t *testing.T, repoDir string) string {
	t.Helper()
	repo, err := gogit.PlainOpen(repoDir)
	require.NoError(t, err)
	ref, err := repo.Head()
	require.NoError(t, err)
	return ref.Hash().String()
}

// fakeGitHubAPI serves the GitHub endpoints needed by InstallationCache +
// GetBranchHead.
func fakeGitHubAPI(t *testing.T, headSHA string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// POST /app/installations/{id}/access_tokens
	mux.HandleFunc("/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, `{"token":"ghs_e2etest","expires_at":%q}`, expires)
	})

	// GET /repos/fake/repo/branches/main
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"commit":{"sha":%q}}`, headSHA)
	})

	return httptest.NewServer(mux)
}

// fakeOpenAI serves a minimal OpenAI-compatible streaming chat endpoint.
func fakeOpenAI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		chunks := []string{
			`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hello from CronFoundry!"},"index":0}],"usage":null}`,
			`data: {"id":"c2","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprint(w, chunk+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
}

func buildBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "cronfoundry")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/cronfoundry")
	cmd.Dir = moduleRoot(t)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "build failed")
	return out
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	require.NoError(t, err)
	// Walk up from this file to find go.mod.
	dir, err := filepath.Abs(".")
	require.NoError(t, err)
	_ = out
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func adminInitGetKey(t *testing.T, binPath string, env map[string]string) string {
	t.Helper()
	cmd := exec.Command(binPath, "admin", "init")
	cmd.Env = envSlice(env)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "admin init (no key): %s", out)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "  CRONFOUNDRY_MASTER_KEY=") {
			return strings.TrimPrefix(strings.TrimSpace(line), "CRONFOUNDRY_MASTER_KEY=")
		}
	}
	t.Fatalf("could not parse master key from output:\n%s", out)
	return ""
}

func runAdminCmd(t *testing.T, binPath string, env map[string]string, stdin io.Reader, args ...string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = envSlice(env)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s: %s", strings.Join(args, " "), out)
}

func lookupScheduleID(t *testing.T, dsn string) string {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	defer pool.Close()
	var id string
	err = pool.QueryRow(context.Background(), `SELECT id FROM schedule LIMIT 1`).Scan(&id)
	require.NoError(t, err, "no schedule row found — did trigger-sync succeed?")
	return id
}

func triggerRunNow(t *testing.T, baseURL, scheduleID string) string {
	t.Helper()
	resp, err := http.Post(
		baseURL+"/internal/schedules/"+scheduleID+"/run-now",
		"application/json",
		strings.NewReader("{}"),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "run-now failed")
	var body struct {
		RunID string `json:"run_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body.RunID)
	return body.RunID
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func copyEnv(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
