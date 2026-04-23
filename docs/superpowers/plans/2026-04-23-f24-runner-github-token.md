# F24 — Inject GitHub Installation Token at Dispatch Time

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Inject a GitHub installation token as `GITHUB_TOKEN` into runner env vars at dispatch time, so `github-issue` destinations and writebacks succeed.

**Architecture:** The scheduler already mints JWTs and builds env vars in `dispatchRun()`. We add `InstallationID` to `dispatchArgs`, resolve it from `repo_connection` via the existing join chain, call `InstallationCache.Token()`, and append `GITHUB_TOKEN` to the env. The runner's github-issue publisher already falls back to a `fallbackToken` — we wire `os.Getenv("GITHUB_TOKEN")` into that slot. Graceful degradation: if token minting fails, dispatch proceeds without it (same `partial_failure` as today).

**Tech Stack:** Go, sqlc (Postgres), existing `github.InstallationCache`

---

### Task 1: Add `Installations` to scheduler `Deps` and wire in `serve.go`

**Files:**
- Modify: `internal/scheduler/tick.go:22-29` (Deps struct)
- Modify: `cmd/cronfoundry/serve.go:235-242` (schedDeps construction)
- Modify: `internal/scheduler/tick_test.go` (all test Deps)

- [ ] **Step 1: Define an interface for the installation token provider**

The scheduler shouldn't depend on the concrete `*github.InstallationCache`. Add an interface in `tick.go` alongside the `Deps` struct:

```go
// InstallationTokenProvider mints short-lived GitHub installation tokens.
type InstallationTokenProvider interface {
	Token(ctx context.Context, installID int64) (string, error)
}
```

Add the field to `Deps`:

```go
type Deps struct {
	Pool           *pgxpool.Pool
	Signer         *token.Signer
	Dispatcher     cloud.JobDispatcher
	Installations  InstallationTokenProvider // nil = no GitHub token injection
	APIBaseURL     string
	RunnerAPIURL   string
	RunnerBinary   string
}
```

- [ ] **Step 2: Wire `installs` into `schedDeps` in `serve.go`**

In `cmd/cronfoundry/serve.go`, the `schedDeps` block (around line 235):

```go
schedDeps := scheduler.Deps{
	Pool:           pool,
	Signer:         signer,
	Dispatcher:     dispatcher,
	Installations:  installs,
	APIBaseURL:     "http://" + addr,
	RunnerAPIURL:   runnerAPIURL,
	RunnerBinary:   self,
}
```

- [ ] **Step 3: Verify existing tests still compile**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go build ./...`
Expected: compiles (Installations is a pointer/interface; nil zero-value is fine for existing tests).

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/tick.go cmd/cronfoundry/serve.go
git commit -m "feat(f24): add InstallationTokenProvider to scheduler Deps"
```

---

### Task 2: Resolve installation ID at dispatch time

Both dispatch paths (`processOne` and `dispatchPending`) need the installation ID. The join chain is `schedule → skill → repo_connection.github_app_install_id`.

**Files:**
- Modify: `internal/db/queries/schedule.sql` (extend `ListDueSchedulesWithSha`)
- Regenerate: `internal/db/gen/schedule.sql.go` (via `sqlc generate`)
- Modify: `internal/scheduler/tick.go` (both dispatch call sites)

- [ ] **Step 1: Extend `ListDueSchedulesWithSha` SQL query**

In `internal/db/queries/schedule.sql`, update the query at line ~91:

```sql
-- name: ListDueSchedulesWithSha :many
-- Like ListDueSchedules but joins the skill to include current_sha so
-- the scheduler can set it on the new run row without a second query.
-- Also joins repo_connection for the installation ID needed at dispatch.
SELECT s.*,
       sk.current_sha AS skill_sha,
       rc.github_app_install_id AS install_id
FROM schedule s
JOIN skill sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE s.enabled = true
  AND s.next_fire_at IS NOT NULL
  AND s.next_fire_at <= now()
ORDER BY s.next_fire_at ASC;
```

- [ ] **Step 2: Regenerate sqlc**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree/internal/db && sqlc generate`
Expected: `gen/schedule.sql.go` updated with new `InstallID int64` field on `ListDueSchedulesWithShaRow`.

If `sqlc` is not installed, manually add the field to `ListDueSchedulesWithShaRow` and the scan in `ListDueSchedulesWithSha`, then update the SQL constant string. The field goes at the end:

```go
// In ListDueSchedulesWithShaRow, add after SkillSha:
InstallID int64
```

And in the Scan call, add `&i.InstallID` at the end.

- [ ] **Step 3: Add `InstallID` to `dispatchArgs` and pass it from `processOne`**

In `tick.go`, extend `dispatchArgs`:

```go
type dispatchArgs struct {
	RunID         pgtype.UUID
	OrgID         pgtype.UUID
	TimeoutSec    int32
	SecretRefs    []string
	InstallID     int64
}
```

In `processOne`, pass `InstallID: sched.InstallID` in the `dispatchArgs` literal (around line 173):

```go
if err := dispatchRun(ctx, deps, dispatchArgs{
	RunID:      run.ID,
	OrgID:      sched.OrgID,
	TimeoutSec: sched.TimeoutSec,
	SecretRefs: config.CollectSecretRefs(sched.DestinationsJson, sched.EnvJson, sched.LlmSecretRef),
	InstallID:  sched.InstallID,
}); err != nil {
```

- [ ] **Step 4: Extend `dispatchPending` raw SQL to also join `repo_connection`**

In `dispatchPending` (tick.go ~267), update the SQL and scan to include `install_id`:

```go
rows, err := deps.Pool.Query(ctx, `
	SELECT r.id, r.org_id,
	       s.timeout_sec, s.destinations_json, s.env_json, s.llm_secret_ref,
	       rc.github_app_install_id
	FROM run r
	JOIN schedule s ON s.id = r.schedule_id
	JOIN skill sk ON sk.id = s.skill_id
	JOIN repo_connection rc ON rc.id = sk.repo_id
	WHERE r.status = 'pending'
	  AND s.enabled = true
	  AND (
	      r.fire_reason = 'manual'
	      OR (
	          r.fire_reason = 'schedule'
	          AND s.overlap_policy = 'queue'
	          AND NOT EXISTS (
	              SELECT 1 FROM run r2
	              WHERE r2.schedule_id = r.schedule_id
	                AND r2.id != r.id
	                AND r2.status IN ('pending','running')
	                AND r2.created_at < r.created_at
	          )
	      )
	  )
	ORDER BY r.created_at ASC
`)
```

Update the `pendingRow` struct and scan:

```go
type pendingRow struct {
	ID         pgtype.UUID
	OrgID      pgtype.UUID
	TimeoutSec int32
	SecretRefs []string
	InstallID  int64
}
// ...
var installID int64
if err := rows.Scan(&r.ID, &r.OrgID, &r.TimeoutSec, &destsJSON, &envJSON, &llmRef, &installID); err != nil {
// ...
r.InstallID = installID
```

And pass it through:

```go
if err := dispatchRun(ctx, deps, dispatchArgs{
	RunID:      r.ID,
	OrgID:      r.OrgID,
	TimeoutSec: r.TimeoutSec,
	SecretRefs: r.SecretRefs,
	InstallID:  r.InstallID,
}); err != nil {
```

- [ ] **Step 5: Verify compilation**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go build ./...`
Expected: compiles. The InstallID is carried through but not yet used in `dispatchRun`.

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/schedule.sql internal/db/gen/schedule.sql.go internal/scheduler/tick.go
git commit -m "feat(f24): carry installation ID through to dispatchArgs"
```

---

### Task 3: Mint token in `dispatchRun()` and inject `GITHUB_TOKEN`

**Files:**
- Modify: `internal/scheduler/tick.go` (`dispatchRun` function)

- [ ] **Step 1: Add token minting to `dispatchRun`**

In `dispatchRun`, after the runner URL resolution block (~line 225) and before building the `spec`, add:

```go
var githubToken string
if deps.Installations != nil && args.InstallID != 0 {
	tok, err := deps.Installations.Token(ctx, args.InstallID)
	if err != nil {
		slog.Warn("scheduler: mint GitHub token failed (dispatch continues without GITHUB_TOKEN)",
			"run_id", uuid.UUID(args.RunID.Bytes).String(),
			"install_id", args.InstallID,
			"err", err)
	} else {
		githubToken = tok
	}
}
```

Then modify the `Env` slice construction to conditionally include it:

```go
env := []string{
	"CRONFOUNDRY_API_URL=" + runnerURL,
	"CRONFOUNDRY_RUN_ID=" + uuid.UUID(args.RunID.Bytes).String(),
	"CRONFOUNDRY_RUN_TOKEN=" + tok,
}
if githubToken != "" {
	env = append(env, "GITHUB_TOKEN="+githubToken)
}

spec := cloud.DispatchRequest{
	BinaryPath: deps.RunnerBinary,
	Args:       []string{"runner", "--run-id", uuid.UUID(args.RunID.Bytes).String()},
	Env:        env,
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/scheduler/tick.go
git commit -m "feat(f24): mint GitHub token at dispatch and inject GITHUB_TOKEN"
```

---

### Task 4: Wire `GITHUB_TOKEN` into the HTTP-mode runner's publisher

**Files:**
- Modify: `cmd/cronfoundry/runner.go:182` (publisher construction)

- [ ] **Step 1: Pass `os.Getenv("GITHUB_TOKEN")` as the fallback token**

In `cmd/cronfoundry/runner.go`, around line 182, change:

```go
"github-issue": publish.NewGitHubIssuePublisher("", ""),
```

to:

```go
"github-issue": publish.NewGitHubIssuePublisher("", os.Getenv("GITHUB_TOKEN")),
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add cmd/cronfoundry/runner.go
git commit -m "feat(f24): pass GITHUB_TOKEN to github-issue publisher in HTTP-mode runner"
```

---

### Task 5: Unit tests for token injection in `dispatchRun`

**Files:**
- Modify: `internal/scheduler/tick_test.go`

- [ ] **Step 1: Add a mock `InstallationTokenProvider`**

```go
type mockInstalls struct {
	token string
	err   error
	calls []int64
	mu    sync.Mutex
}

func (m *mockInstalls) Token(_ context.Context, installID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, installID)
	return m.token, m.err
}
```

- [ ] **Step 2: Update `seedDueSchedule` to use a known install ID**

The existing seed uses `github_app_install_id = 1`. That's fine — our tests will assert on install ID `1`.

- [ ] **Step 3: Write test — token appears in dispatch env when minting succeeds**

```go
func TestTick_InjectsGitHubToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	seedDueSchedule(t, pool, "skip")
	mock := &mockDispatcher{}
	installs := &mockInstalls{token: "ghs_test_token_123"}

	deps := Deps{
		Pool:          pool,
		Signer:        newSigner(t),
		Dispatcher:    mock,
		Installations: installs,
		APIBaseURL:    "http://127.0.0.1:8080",
		RunnerBinary:  "/usr/bin/true",
	}

	stats, err := Tick(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Dispatched)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	require.Len(t, mock.calls, 1)

	var hasGHToken bool
	for _, e := range mock.calls[0].Env {
		if e == "GITHUB_TOKEN=ghs_test_token_123" {
			hasGHToken = true
		}
	}
	assert.True(t, hasGHToken, "GITHUB_TOKEN should be in dispatch env vars; got: %v", mock.calls[0].Env)

	installs.mu.Lock()
	defer installs.mu.Unlock()
	assert.Equal(t, []int64{1}, installs.calls, "should have called Token with install_id=1")
}
```

- [ ] **Step 4: Write test — dispatch proceeds without `GITHUB_TOKEN` when minting fails**

```go
func TestTick_DispatchesWithoutTokenOnMintError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	seedDueSchedule(t, pool, "skip")
	mock := &mockDispatcher{}
	installs := &mockInstalls{err: fmt.Errorf("GitHub API down")}

	deps := Deps{
		Pool:          pool,
		Signer:        newSigner(t),
		Dispatcher:    mock,
		Installations: installs,
		APIBaseURL:    "http://127.0.0.1:8080",
		RunnerBinary:  "/usr/bin/true",
	}

	stats, err := Tick(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Dispatched, "should still dispatch even when token minting fails")

	mock.mu.Lock()
	defer mock.mu.Unlock()
	require.Len(t, mock.calls, 1)

	for _, e := range mock.calls[0].Env {
		assert.False(t, strings.HasPrefix(e, "GITHUB_TOKEN="),
			"GITHUB_TOKEN should NOT be in env when minting failed; got: %s", e)
	}
}
```

- [ ] **Step 5: Write test — dispatch works with nil Installations (backwards compat)**

```go
func TestTick_NilInstallationsStillDispatches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	seedDueSchedule(t, pool, "skip")
	mock := &mockDispatcher{}

	deps := Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   mock,
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	}

	stats, err := Tick(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Dispatched)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	require.Len(t, mock.calls, 1)
	for _, e := range mock.calls[0].Env {
		assert.False(t, strings.HasPrefix(e, "GITHUB_TOKEN="),
			"GITHUB_TOKEN should not appear when Installations is nil")
	}
}
```

- [ ] **Step 6: Run tests**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go test ./internal/scheduler/ -run "TestTick_Injects|TestTick_DispatchesWithout|TestTick_NilInstallations" -v`
Expected: all three pass.

- [ ] **Step 7: Run full scheduler test suite**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go test ./internal/scheduler/ -v`
Expected: all existing tests pass (they use nil `Installations` which is the backwards-compat path).

- [ ] **Step 8: Commit**

```bash
git add internal/scheduler/tick_test.go
git commit -m "test(f24): verify GitHub token injection at dispatch time"
```

---

### Task 6: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go test ./... 2>&1 | tail -30`
Expected: all tests pass.

- [ ] **Step 2: Verify build produces a working binary**

Run: `cd /home/tng/workspace/cronfoundry-f24-worktree && go build -o /dev/null ./cmd/cronfoundry`
Expected: clean build.

- [ ] **Step 3: Commit any remaining changes and push**

```bash
git push -u origin fix/f24-runner-github-token
```
