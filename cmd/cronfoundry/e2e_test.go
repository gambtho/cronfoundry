//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/testdb"
)

// TestE2E_FullScheduleFire is the end-to-end integration test for
// cronfoundry serve. It boots a throwaway Postgres, builds the binary,
// seeds the DB directly (skipping GitHub sync), inserts a manual pending
// run, starts serve, and polls until the run reaches a terminal status.
//
// The run will reach "failed" status because the clone step cannot reach
// github.com in a test environment — this is intentional. The test verifies
// the full scheduler→runner-subprocess→finalize pathway.
func TestE2E_FullScheduleFire(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in short mode")
	}

	ctx := context.Background()

	// ── 1. Boot throwaway Postgres ──────────────────────────────────────────
	dsn, teardownDB := testdb.BootPGWithDSN(t)
	defer teardownDB()

	// ── 2. Build the cronfoundry binary ─────────────────────────────────────
	binPath := buildBinary(t)

	// ── 3. Generate a master key using admin init (first pass) ──────────────
	// First invocation without CRONFOUNDRY_MASTER_KEY prints a key and exits.
	masterKey := generateMasterKey(t, binPath, dsn)

	// ── 4. Run admin init to create schema + org ─────────────────────────────
	runAdminInitWithKey(t, binPath, dsn, masterKey)

	// ── 5. Seed DB rows directly ─────────────────────────────────────────────
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	require.NoError(t, err)

	// Insert a fake repo connection (owner=e2etest, name=fixtures, installID=12345).
	repoRow, err := q.InsertRepoConnection(ctx, dbgen.InsertRepoConnectionParams{
		OrgID:              org.ID,
		GithubAppInstallID: 12345,
		Owner:              "e2etest",
		Name:               "fixtures",
		DefaultBranch:      "main",
		SyncIntervalSec:    3600,
	})
	require.NoError(t, err)

	// Insert a skill.
	skillRow, err := q.UpsertSkill(ctx, dbgen.UpsertSkillParams{
		OrgID:           org.ID,
		RepoID:          repoRow.ID,
		Path:            "skills/hello",
		Name:            "hello",
		CurrentSha:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		FrontmatterJson: []byte(`{"name":"hello","description":"e2e test skill"}`),
	})
	require.NoError(t, err)

	// Build destinations JSON with a fake Discord webhook secret.
	destsJSON := []byte(`[{"type":"discord","discord":{"secret":"discord_webhook"}}]`)

	// Insert a schedule.
	llmRef := "openai_key"
	schedRow, err := q.UpsertSchedule(ctx, dbgen.UpsertScheduleParams{
		OrgID:            org.ID,
		SkillID:          skillRow.ID,
		Name:             "e2e-test",
		Cron:             "0 0 1 1 *", // never fires naturally
		Timezone:         "UTC",
		OverlapPolicy:    "skip",
		TimeoutSec:       120,
		Enabled:          true,
		Provider:         "openai",
		Model:            "gpt-4o-mini",
		LlmSecretRef:     &llmRef,
		DestinationsJson: destsJSON,
		WritebackJson:    []byte(`null`),
		EnvJson:          []byte(`{}`),
	})
	require.NoError(t, err)

	// ── 6. Store fake secrets via secretstore ────────────────────────────────
	master, err := secretstore.ParseMasterKey(masterKey)
	require.NoError(t, err)
	store := secretstore.NewEnvelopePostgresStore(pool, org.ID, master)
	require.NoError(t, store.Put(ctx, "openai_key", "sk-fake-e2e-key"))
	require.NoError(t, store.Put(ctx, "discord_webhook", "http://"+fakeDiscordWebhookServer(t).Listener.Addr().String()+"/discord"))

	// ── 7. Generate a test RSA PEM for GitHub App ────────────────────────────
	pemPath := generateTestPEMFile(t)

	// ── 8. Start a fake GitHub API (for installation token endpoint) ─────────
	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// POST /app/installations/:id/access_tokens
		if strings.Contains(r.URL.Path, "/access_tokens") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":      "ghs_fake_installation_token",
				"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ghServer.Close()

	// ── 9. Start a fake OpenAI API ────────────────────────────────────────────
	// Serves a minimal streaming SSE response for chat completions.
	openaiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			// Write a single SSE chunk + [DONE].
			chunk := map[string]any{
				"id":     "chatcmpl-e2e",
				"object": "chat.completion.chunk",
				"choices": []map[string]any{
					{
						"index": 0,
						"delta": map[string]any{"role": "assistant", "content": "e2e done"},
					},
				},
				"usage": map[string]any{
					"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
				},
			}
			chunkJSON, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer openaiServer.Close()

	// ── 10. Find a free port for the serve API ───────────────────────────────
	apiAddr := freeAddr(t)
	apiBaseURL := "http://" + apiAddr

	// ── 11. Start cronfoundry serve as a subprocess ──────────────────────────
	serveEnv := buildEnvMap(map[string]string{
		"CRONFOUNDRY_DATABASE_URL":    dsn,
		"CRONFOUNDRY_MASTER_KEY":      masterKey,
		"CRONFOUNDRY_GITHUB_APP_ID":   "123456",
		"CRONFOUNDRY_GITHUB_APP_PEM":  pemPath,
		"CRONFOUNDRY_GITHUB_BASE_URL": ghServer.URL,
		"CRONFOUNDRY_OPENAI_BASE_URL": openaiServer.URL,
		// Shorten the tick so the test is fast.
		"HOME": os.Getenv("HOME"),
		"PATH": os.Getenv("PATH"),
	})

	serveCmd := exec.Command(binPath, "serve",
		"--addr", apiAddr,
		"--tick-cadence", "2s",
	)
	serveCmd.Env = serveEnv
	serveCmd.Stdout = os.Stderr // route serve logs to test output
	serveCmd.Stderr = os.Stderr
	require.NoError(t, serveCmd.Start())
	defer func() {
		_ = serveCmd.Process.Kill()
		_ = serveCmd.Wait()
	}()

	// Wait for the API to be healthy.
	waitHealthy(t, apiBaseURL+"/healthz", 15*time.Second)
	t.Log("e2e: serve is healthy")

	// ── 12. Insert a manual pending run ─────────────────────────────────────
	// This is how POST /internal/schedules/{id}/run-now would work — insert
	// a run row with fire_reason='manual' and fire_time=NULL, leave it
	// pending. The scheduler's dispatchPending loop will pick it up on the
	// next tick.
	runID := insertManualRun(t, ctx, pool, org.ID, schedRow.ID, skillRow.CurrentSha)
	t.Logf("e2e: inserted manual run %s", runID)

	// ── 13. Poll until the run reaches a terminal status ─────────────────────
	terminalStatuses := map[string]bool{
		"succeeded":       true,
		"partial_failure": true,
		"failed":          true,
	}

	deadline := time.Now().Add(3 * time.Minute)
	var finalStatus string
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		run, err := q.GetRun(ctx, runID)
		if err != nil {
			t.Logf("e2e: GetRun err (may be transient): %v", err)
			continue
		}
		t.Logf("e2e: run status = %s", run.Status)
		if terminalStatuses[run.Status] {
			finalStatus = run.Status
			break
		}
	}

	require.NotEmpty(t, finalStatus, "run did not reach terminal status within deadline")
	t.Logf("e2e: run reached terminal status %q", finalStatus)

	// ── 14. Check run events (informational) ────────────────────────────────
	// Events are stored by the runner AFTER clone. If the run fails at clone
	// (e.g., network unreachable in CI), no events are persisted — that path
	// skips PostEvents and goes directly to PostFinalize.
	// We log events if present but do not assert on count.
	events, err := q.ListRunEvents(ctx, runID)
	require.NoError(t, err)
	t.Logf("e2e: %d run event(s) stored", len(events))
	for _, ev := range events {
		t.Logf("e2e: run event type=%s level=%s", ev.EventType, ev.Level)
	}

	// Core assertion: the run reached a terminal state (not stuck pending/running).
	t.Logf("e2e: PASS — run %s finalized with status %q", runID.String(), finalStatus)
}

// ── helpers ─────────────────────────────────────────────────────────────────

// buildBinary compiles the cronfoundry binary and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "cronfoundry")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "build failed: %s", out)
	return binPath
}

// generateMasterKey runs `cronfoundry admin init` without a master key env var
// to print a fresh key, then parses it from stdout.
func generateMasterKey(t *testing.T, binPath, dsn string) string {
	t.Helper()
	cmd := exec.Command(binPath, "admin", "init")
	cmd.Env = []string{
		"CRONFOUNDRY_DATABASE_URL=" + dsn,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	// admin init without MASTER_KEY exits 0 and prints the key — it doesn't error.
	require.NoError(t, err, "admin init (key gen) failed: %s", out)
	output := string(out)
	// Parse "CRONFOUNDRY_MASTER_KEY=<value>" from the output.
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CRONFOUNDRY_MASTER_KEY=") {
			return strings.TrimPrefix(line, "CRONFOUNDRY_MASTER_KEY=")
		}
	}
	t.Fatalf("could not parse master key from admin init output:\n%s", output)
	return ""
}

// runAdminInitWithKey runs `cronfoundry admin init` with the master key set,
// which runs migrations and seeds the org.
func runAdminInitWithKey(t *testing.T, binPath, dsn, masterKey string) {
	t.Helper()
	cmd := exec.Command(binPath, "admin", "init")
	cmd.Env = []string{
		"CRONFOUNDRY_DATABASE_URL=" + dsn,
		"CRONFOUNDRY_MASTER_KEY=" + masterKey,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "admin init (with key) failed: %s", out)
	t.Logf("admin init output: %s", strings.TrimSpace(string(out)))
}

// generateTestPEMFile generates a 2048-bit RSA private key, writes it to a
// temp file in PEM format, and returns the file path.
func generateTestPEMFile(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	f, err := os.CreateTemp(t.TempDir(), "test-*.pem")
	require.NoError(t, err)
	_, err = f.Write(pemBytes)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// freeAddr returns a localhost address with an available port.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// waitHealthy polls the given URL until it returns 200 or the timeout expires.
func waitHealthy(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become healthy within %s", url, timeout)
}

// fakeDiscordWebhookServer starts a throwaway HTTP server that records Discord
// webhook POSTs. Returns the server so the caller can check Requests.
func fakeDiscordWebhookServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("e2e: fake discord received %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildEnvMap converts a map to a []string "KEY=VALUE" slice suitable for
// exec.Cmd.Env. The caller supplies only the vars it cares about; the rest
// of the process environment is NOT inherited (intentional isolation).
func buildEnvMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// insertManualRun inserts a pending manual run row for the given schedule.
// Returns the run UUID as a pgtype.UUID.
func insertManualRun(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, scheduleID pgtype.UUID,
	skillSha string,
) pgtype.UUID {
	t.Helper()
	// InsertRun with fire_time=NULL → bypasses the partial unique index.
	// fire_reason='manual' → dispatchPending will pick it up.
	row, err := dbgen.New(pool).InsertRun(ctx, dbgen.InsertRunParams{
		OrgID:      orgID,
		ScheduleID: scheduleID,
		SkillSha:   skillSha,
		FireTime:   pgtype.Timestamptz{Valid: false}, // NULL → manual run
		FireReason: "manual",
		Actor:      nil,
		// placeholder hash; dispatchRun will overwrite it
		RunnerTokenHash: "pending:e2e-placeholder",
	})
	require.NoError(t, err)
	return row.ID
}
