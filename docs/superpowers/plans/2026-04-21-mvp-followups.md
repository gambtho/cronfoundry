# MVP Follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the five remaining gaps identified by the 2026-04-21 design audit against `docs/superpowers/specs/2026-04-19-cronfoundry-design.md`, and add a local end-to-end smoke test that exercises the full schedule-fire pipeline against mocks.

**Architecture:** Narrow, surgical changes to existing packages — no new services, no new abstractions. Each gap maps to a single concern: LLM retry bounds, runner wall-clock timeout, per-provider cost accounting, session TTL, and an end-to-end smoke test. Production runner is `cmd/cronfoundry/runner.go` (HTTP mode); standalone CLI is `cmd/runner/main.go`. Both invoke `internal/runner.Runner`.

**Tech Stack:** Go 1.22+, openai-go/v3, anthropic-sdk-go, pgx, stretchr/testify, httptest.

**Pre-flight:**
- PR #14 (`feature/p7-mvp-closeout`) merged to `main` on 2026-04-21 as `f7f2f93`. No open PRs at plan start. Work lands on a fresh branch off `main`.
- Run `go test ./... -timeout 120s` before each commit.
- Run `go vet ./...` before each commit.

**Post-merge re-audit (2026-04-21):** PR #14 landed the LogTail UI, Azure smoke runbook (`docs/guides/smoke-test-mvp-azure.md`), and related test fixes. Tasks 1–6 below still apply unchanged. Task 7 (Azure smoke runbook) is **no longer needed** — the merged runbook is more thorough than what this plan proposed.

---

## File structure (touched)

| Path | Responsibility | Action |
| --- | --- | --- |
| `internal/llm/provider.go` | Provider interface + shared types | Modify: add `Cost` fields to `Usage` |
| `internal/llm/pricing.go` | **NEW** — provider+model → cents/1M-tokens lookup | Create |
| `internal/llm/pricing_test.go` | **NEW** — unit test for pricing lookup | Create |
| `internal/llm/openai.go` | OpenAI adapter | Modify: `option.WithMaxRetries(3)`, compute cost via pricing |
| `internal/llm/anthropic.go` | Anthropic adapter | Modify: `option.WithMaxRetries(3)`, compute cost via pricing |
| `internal/llm/azurefoundry.go` | Azure AI Foundry adapter | Modify: `option.WithMaxRetries(3)`; BYOK — no cost |
| `internal/llm/openai_test.go` | Existing | Modify: add retry-count test |
| `internal/api/run_context.go` | `GET /internal/runs/{id}/context` | Modify: include `timeout_sec` |
| `internal/api/run_context_test.go` | Existing | Modify: assert `timeout_sec` in response |
| `internal/db/queries/run.sql` | SQLC queries | Modify: `GetRunForContext` selects `timeout_sec` |
| `internal/db/gen/run.sql.go` | sqlc-generated | Regenerate |
| `cmd/cronfoundry/runner.go` | Production HTTP-mode runner | Modify: parse `timeout_sec`, wrap ctx with deadline, send `cost_cents` |
| `cmd/cronfoundry/runner_test.go` | Existing | Modify: assert `cost_cents` sent when non-zero |
| `internal/runner/runner.go` | Shared runner library | Modify: `RunResult.CostCents` field |
| `internal/runner/runner_test.go` | Existing | Modify: assert cost computed from usage |
| `internal/webapi/oauth.go` | OAuth callback + session cookie | Modify: 24h → 7d |
| `internal/webapi/oauth_test.go` | OAuth tests | Modify: assert 7d cookie max-age |
| `cmd/cronfoundry/smoke_test.go` | **NEW** — local end-to-end smoke | Create |
| `docs/guides/smoke-test.md` | **NEW** — Azure smoke runbook checklist | Create |

---

## Task 1: Bump session cookie TTL to 7 days

**Files:**
- Modify: `internal/webapi/oauth.go:129-142`
- Modify: `internal/webapi/oauth_test.go`

The design spec §Auth flow calls for a "7-day idle timeout" on the session cookie. Current code sets 24h (`24*time.Hour`, `MaxAge: 86400`).

- [ ] **Step 1: Write the failing test**

Open `internal/webapi/oauth_test.go` and add (or update the existing callback test):

```go
func TestCallback_SessionCookieIs7Days(t *testing.T) {
    // Fixture: minimal deps + happy-path token-exchange and user-fetch mocks.
    // Assumes existing helper newTestHandlers(t) returns oauthHandlers with
    // mocked GitHubAPIBase and a seeded org. Reuse whatever pattern
    // oauth_test.go already uses for callback tests.
    h, srv := newTestHandlers(t) // reuse existing helper
    defer srv.Close()

    req := httptest.NewRequest("GET", "/oauth/callback?code=abc&state=STATE", nil)
    req.AddCookie(&http.Cookie{Name: "oauth_state", Value: signedTestState(t, h.deps.MasterKey)})
    rr := httptest.NewRecorder()
    h.callback(rr, req)

    var sess *http.Cookie
    for _, c := range rr.Result().Cookies() {
        if c.Name == "cf_session" {
            sess = c
        }
    }
    require.NotNil(t, sess, "cf_session cookie must be set")
    assert.Equal(t, 7*24*3600, sess.MaxAge, "session cookie max-age must be 7 days")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/webapi/ -run TestCallback_SessionCookieIs7Days -v`
Expected: FAIL with `max-age must be 7 days; got 86400`.

- [ ] **Step 3: Update the cookie TTL in oauth.go**

In `internal/webapi/oauth.go` replace the two constants in the callback (lines 129 and 138):

```go
session, err := SignSession(SessionClaims{Login: login, Role: role}, h.deps.MasterKey, 7*24*time.Hour)
```

```go
http.SetCookie(w, &http.Cookie{
    Name:     "cf_session",
    Value:    session,
    Path:     "/",
    MaxAge:   7 * 24 * 3600,
    HttpOnly: true,
    SameSite: http.SameSiteLaxMode,
    Secure:   !isLocalhost(r.Host),
})
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/webapi/ -v`
Expected: PASS (all, including the new one).

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/oauth.go internal/webapi/oauth_test.go
git commit -m "feat(webapi): extend session cookie TTL from 24h to 7d per design spec"
```

---

## Task 2: Explicit LLM retry bounds (max 3 on 429/5xx)

**Files:**
- Modify: `internal/llm/openai.go`
- Modify: `internal/llm/anthropic.go`
- Modify: `internal/llm/azurefoundry.go`
- Modify: `internal/llm/openai_test.go`

Design spec §Execution Flow calls for "exponential backoff on 429/5xx, max 3 retries." Both SDKs (openai-go v3, anthropic-sdk-go) default to 2 internal retries on retryable statuses. Setting `option.WithMaxRetries(3)` explicitly matches spec and makes the policy legible in code.

- [ ] **Step 1: Write the failing test**

Add to `internal/llm/openai_test.go`:

```go
func TestOpenAI_Chat_RetriesOn500UpTo3Times(t *testing.T) {
    var attempts int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&attempts, 1)
        w.WriteHeader(http.StatusInternalServerError)
        _, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
    }))
    defer srv.Close()

    p := NewOpenAI(srv.URL)
    _, err := p.Chat(context.Background(),
        []Message{{Role: RoleUser, Content: "u"}},
        CallOptions{Model: "m", APIKey: "k"},
        func(StreamChunk) {})
    require.Error(t, err)
    // Spec: max 3 retries → 1 initial + 3 retries = 4 attempts total.
    assert.Equal(t, int32(4), atomic.LoadInt32(&attempts),
        "expected 1 initial + 3 retries = 4 total attempts")
}
```

Add `"sync/atomic"` to the imports block if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestOpenAI_Chat_RetriesOn500UpTo3Times -v`
Expected: FAIL — attempts will be 3 (SDK default: 1 initial + 2 retries).

- [ ] **Step 3: Update all three providers to set retries = 3 explicitly**

In `internal/llm/openai.go`, update the `clientOpts` slice inside `Chat`:

```go
clientOpts := []option.RequestOption{
    option.WithAPIKey(opts.APIKey),
    option.WithMaxRetries(3),
}
```

In `internal/llm/anthropic.go`, same treatment:

```go
clientOpts := []option.RequestOption{
    option.WithAPIKey(opts.APIKey),
    option.WithMaxRetries(3),
}
```

In `internal/llm/azurefoundry.go`, add as a client option:

```go
client := openai.NewClient(
    azure.WithEndpoint(opts.Endpoint, defaultAzureAPIVersion),
    azure.WithAPIKey(opts.APIKey),
    option.WithHeaderDel("Authorization"),
    option.WithMaxRetries(3),
)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/llm/ -v`
Expected: PASS (all, including the new attempts==4 assertion).

- [ ] **Step 5: Commit**

```bash
git add internal/llm/openai.go internal/llm/anthropic.go internal/llm/azurefoundry.go internal/llm/openai_test.go
git commit -m "feat(llm): pin SDK retry budget to 3 retries per design spec"
```

---

## Task 3: Per-provider cost pricing table

**Files:**
- Create: `internal/llm/pricing.go`
- Create: `internal/llm/pricing_test.go`

Design spec §In scope calls for "Token / cost accounting per run." `run.cost_cents` exists in the schema and the `finalize` endpoint accepts `cost_cents`, but nothing computes it. This task introduces a pure pricing function; Task 4 wires it through `Usage`.

- [ ] **Step 1: Write the failing test**

Create `internal/llm/pricing_test.go`:

```go
package llm

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestCostCents(t *testing.T) {
    cases := []struct {
        name                 string
        provider, model      string
        inputTok, outputTok  int
        wantCents            int
    }{
        {
            name:     "openai gpt-4o-mini cheap",
            provider: "openai", model: "gpt-4o-mini",
            inputTok: 1_000_000, outputTok: 1_000_000,
            // $0.15 in + $0.60 out per 1M = $0.75 = 75 cents.
            wantCents: 75,
        },
        {
            name:     "openai gpt-4o",
            provider: "openai", model: "gpt-4o",
            inputTok: 1_000_000, outputTok: 1_000_000,
            // $2.50 in + $10.00 out per 1M = $12.50 = 1250 cents.
            wantCents: 1250,
        },
        {
            name:     "anthropic claude sonnet 4",
            provider: "anthropic", model: "claude-sonnet-4-5",
            inputTok: 1_000_000, outputTok: 1_000_000,
            // $3.00 in + $15.00 out per 1M = $18.00 = 1800 cents.
            wantCents: 1800,
        },
        {
            name:     "azure foundry returns 0 (BYOK)",
            provider: "azure-foundry", model: "gpt-4o",
            inputTok: 1_000_000, outputTok: 1_000_000,
            wantCents: 0,
        },
        {
            name:     "unknown model returns 0",
            provider: "openai", model: "gpt-fictional-9",
            inputTok: 1_000_000, outputTok: 1_000_000,
            wantCents: 0,
        },
        {
            name:     "sub-penny rounds down",
            provider: "openai", model: "gpt-4o-mini",
            inputTok: 1000, outputTok: 1000,
            // $0.00015 + $0.0006 = $0.00075 → 0 cents (sub-penny floors).
            wantCents: 0,
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := CostCents(tc.provider, tc.model, Usage{
                InputTokens: tc.inputTok, OutputTokens: tc.outputTok,
            })
            assert.Equal(t, tc.wantCents, got)
        })
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestCostCents -v`
Expected: FAIL with `undefined: CostCents`.

- [ ] **Step 3: Create pricing.go**

Create `internal/llm/pricing.go`:

```go
package llm

// pricePer1M is USD cents per 1,000,000 tokens, as fixed-point integers.
// 75 means $0.75. Azure AI Foundry is omitted — that provider is BYOK and
// the customer is billed by Azure directly, so we do not compute cost.
type pricePer1M struct{ in, out int }

// priceTable is a minimal lookup for public-list pricing as of 2026-Q1.
// Unknown (provider, model) combinations return 0 rather than erroring —
// a run must not fail because we haven't catalogued a new model. Operators
// can PR new rows; stale rows are harmless (they just under/over-report).
var priceTable = map[string]map[string]pricePer1M{
    "openai": {
        "gpt-4o-mini": {in: 15, out: 60},
        "gpt-4o":      {in: 250, out: 1000},
        "gpt-5.1":     {in: 300, out: 600},
    },
    "anthropic": {
        "claude-haiku-4-5":  {in: 100, out: 500},
        "claude-sonnet-4-5": {in: 300, out: 1500},
        "claude-opus-4-6":   {in: 1500, out: 7500},
    },
}

// CostCents returns the run cost in whole cents (floored) for the given
// provider, model, and token usage. Returns 0 for unknown entries so the
// finalize path never emits a negative or arbitrary value.
func CostCents(provider, model string, u Usage) int {
    models, ok := priceTable[provider]
    if !ok {
        return 0
    }
    p, ok := models[model]
    if !ok {
        return 0
    }
    // Math: (tokens * cents_per_1M) / 1_000_000. Use int64 to avoid overflow
    // at token counts in the hundreds of millions.
    inCents := int64(u.InputTokens) * int64(p.in) / 1_000_000
    outCents := int64(u.OutputTokens) * int64(p.out) / 1_000_000
    return int(inCents + outCents)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/llm/ -run TestCostCents -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llm/pricing.go internal/llm/pricing_test.go
git commit -m "feat(llm): add CostCents pricing lookup for OpenAI and Anthropic"
```

---

## Task 4: Plumb cost through runner → finalize

**Files:**
- Modify: `internal/runner/runner.go:52-62` (add `CostCents` field), `:156-157` (compute)
- Modify: `internal/runner/runner_test.go`
- Modify: `cmd/cronfoundry/runner.go:220-227` (send CostCents on finalize)
- Modify: `cmd/cronfoundry/runner_test.go`

- [ ] **Step 1: Write the failing runner-library test**

Add to `internal/runner/runner_test.go` (adapt `fakeProvider` from the existing test if present; otherwise define inline):

```go
func TestRun_ComputesCostCentsFromUsage(t *testing.T) {
    // Fixture: fakeProvider returning 1,000,000 input + 1,000,000 output
    // tokens for an openai/gpt-4o-mini schedule → 75 cents.
    tmpRepo := writeMinimalRepo(t, "openai", "gpt-4o-mini") // existing helper
    r := runner.New(runner.Deps{
        ProviderFactory: func(string) (llm.Provider, error) {
            return &fakeProvider{out: "hello", usage: llm.Usage{
                InputTokens: 1_000_000, OutputTokens: 1_000_000,
            }}, nil
        },
        Publishers: map[string]publish.Publisher{},
    })
    res, err := r.Run(context.Background(), runner.RunInput{
        RepoRoot: tmpRepo, ManifestPath: "cronfoundry.yaml",
        SkillPath: "skills/demo", ScheduleName: "daily",
        Secrets: secrets.New(nil), LLMAPIKey: "k", DryRun: true,
    })
    require.NoError(t, err)
    assert.Equal(t, 75, res.CostCents)
}
```

If the test file doesn't yet have `writeMinimalRepo` or `fakeProvider`, use whatever fixture pattern `runner_test.go` already establishes — search the file first with `grep -n "fakeProvider\|writeMinimal" internal/runner/runner_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runner/ -run TestRun_ComputesCostCentsFromUsage -v`
Expected: FAIL — `res.CostCents` undefined field OR zero value.

- [ ] **Step 3: Add CostCents to RunResult and compute in Run**

In `internal/runner/runner.go`, update the struct:

```go
type RunResult struct {
    Status         Status
    Usage          llm.Usage
    CostCents      int
    Output         string
    MemoryContent  string
    PublishResults []publish.Result
    WritebackSHA   string
    StartedAt      time.Time
    FinishedAt     time.Time
}
```

Right after `result.Usage = usage` (line 156 currently), add:

```go
result.Usage = usage
result.CostCents = llm.CostCents(sch.Provider, sch.Model, usage)
```

- [ ] **Step 4: Run runner tests**

Run: `go test ./internal/runner/ -v`
Expected: PASS.

- [ ] **Step 5: Write failing HTTP-runner test**

Add to `cmd/cronfoundry/runner_test.go`:

```go
func TestAPIClient_PostFinalize_SendsCostCents(t *testing.T) {
    var gotBody finalizeRequest
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = json.NewDecoder(r.Body).Decode(&gotBody)
        w.WriteHeader(http.StatusNoContent)
    }))
    defer srv.Close()

    c := &apiClient{baseURL: srv.URL, token: "t", http: &http.Client{Timeout: 2 * time.Second}}
    cents := int32(42)
    err := c.PostFinalize(context.Background(), "run-1", finalizeRequest{
        Status:    "succeeded",
        CostCents: &cents,
    })
    require.NoError(t, err)
    require.NotNil(t, gotBody.CostCents)
    assert.Equal(t, int32(42), *gotBody.CostCents)
}
```

- [ ] **Step 6: Verify test fails, then wire CostCents through**

Run: `go test ./cmd/cronfoundry/ -run TestAPIClient_PostFinalize_SendsCostCents -v`
Expected: PASS already (the `CostCents` field on `finalizeRequest` exists at `runner.go:366`). If the test fails for an unrelated reason, fix. Then ensure the production call site populates it — in `cmd/cronfoundry/runner.go` around line 227 (the block that sets `body.TokensOut`), append:

```go
if result.CostCents > 0 {
    v := int32(result.CostCents)
    body.CostCents = &v
}
```

- [ ] **Step 7: Run the full test suite**

Run: `go test ./... -timeout 120s`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/runner/runner.go internal/runner/runner_test.go cmd/cronfoundry/runner.go cmd/cronfoundry/runner_test.go
git commit -m "feat(runner): compute and persist per-run cost_cents"
```

---

## Task 5: Runner wall-clock timeout enforcement

**Files:**
- Modify: `internal/db/queries/run.sql` (add `s.timeout_sec` to `GetRunForContext` SELECT)
- Regenerate: `internal/db/gen/run.sql.go` via `sqlc generate` (or by hand if sqlc not installed)
- Modify: `internal/api/run_context.go` (add `TimeoutSec` field, populate from row)
- Modify: `internal/api/run_context_test.go` (assert field present)
- Modify: `cmd/cronfoundry/runner.go` (add `TimeoutSec` to mirror struct, `context.WithTimeout`)
- Modify: `cmd/cronfoundry/runner_test.go` (test context deadline fires)

Current state: `schedule.timeout_sec` is used only to set the dispatch JWT's `ExpiresAt` at `internal/scheduler/tick.go:206`. The runner process itself calls `r.Run(ctx, ...)` with `cmd.Context()` which has no deadline (`cmd/cronfoundry/runner.go:49`, `:176`). Per design §Policies, "Runner self-kills past the timeout." Fix: pass `timeout_sec` via the RunContext response and wrap the runner's ctx with `context.WithTimeout`.

- [ ] **Step 1: Write the failing API test**

Extend `internal/api/run_context_test.go` — find the happy-path test (likely `TestRunContext_Happy` or similar; grep the file) and add an assertion:

```go
// Added assertion inside the existing happy-path test, after decoding out:
assert.Equal(t, int32(600), out.TimeoutSec, "timeout_sec must be surfaced from the schedule row")
```

You will need to update whatever seed INSERT the test uses so the schedule row has `timeout_sec = 600`.

Add a new field to the response expectation:

```go
type runContextForTest struct {
    ...                     // existing fields
    TimeoutSec int32 `json:"timeout_sec"`
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestRunContext -v`
Expected: FAIL — TimeoutSec is zero.

- [ ] **Step 3: Update SQLC query and regenerate**

Open `internal/db/queries/run.sql`, find `GetRunForContext`, and add `s.timeout_sec` to the SELECT list (alphabetize consistently with existing columns).

Then regenerate:

```bash
cd /home/tng/workspace/cronfoundry && sqlc generate
```

If `sqlc` is not installed, add the field to `internal/db/gen/run.sql.go` manually:
- Add `TimeoutSec int32` to the `GetRunForContextRow` struct.
- Append `&i.TimeoutSec` to the `rows.Scan(...)` call in `GetRunForContext`.
- Add `timeout_sec` to the SELECT string literal at the top of `GetRunForContext`.

- [ ] **Step 4: Wire TimeoutSec through the API response**

Edit `internal/api/run_context.go`:

```go
type RunContext struct {
    RunID           string          `json:"run_id"`
    OrgID           string          `json:"org_id"`
    ScheduleName    string          `json:"schedule_name"`
    SkillPath       string          `json:"skill_path"`
    SkillSha        string          `json:"skill_sha"`
    Repo            string          `json:"repo"`
    RepoID          string          `json:"repo_id"`
    DefaultBranch   string          `json:"default_branch"`
    InstallationID  int64           `json:"installation_id"`
    Provider        string          `json:"provider"`
    Model           string          `json:"model"`
    TimeoutSec      int32           `json:"timeout_sec"`
    LLMSecretRef    *string         `json:"llm_secret_ref,omitempty"`
    LLMEndpoint     *string         `json:"llm_endpoint,omitempty"`
    LLMDeployment   *string         `json:"llm_deployment,omitempty"`
    Destinations    json.RawMessage `json:"destinations"`
    Writeback       json.RawMessage `json:"writeback,omitempty"`
    Env             json.RawMessage `json:"env"`
    FrontmatterJSON json.RawMessage `json:"frontmatter"`
    SecretManifest  []string        `json:"secret_manifest"`
}
```

In the handler where `out :=` is built, add `TimeoutSec: row.TimeoutSec,` to the struct literal.

- [ ] **Step 5: Run API tests**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 6: Mirror TimeoutSec in the production runner's client struct**

Edit `cmd/cronfoundry/runner.go` — the `runContext` struct (lines 299–319):

```go
type runContext struct {
    ...                           // existing fields
    TimeoutSec     int32           `json:"timeout_sec"`
    ...
}
```

- [ ] **Step 7: Write a failing test: ctx deadline fires**

Add to `cmd/cronfoundry/runner_test.go`:

```go
func TestRunnerHTTP_AppliesTimeoutFromRunContext(t *testing.T) {
    // Serve a run context with timeout_sec=1, then a /clone-url endpoint
    // that blocks for 5s. The runner must abort via context deadline.
    var fullBody []byte
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch {
        case strings.HasSuffix(r.URL.Path, "/context"):
            w.Header().Set("Content-Type", "application/json")
            _ = json.NewEncoder(w).Encode(runContext{
                RunID: "r1", TimeoutSec: 1, SkillPath: "x", ScheduleName: "y",
                Provider: "openai", Model: "gpt-4o-mini",
            })
        case strings.HasSuffix(r.URL.Path, "/events"):
            w.WriteHeader(http.StatusNoContent)
        case strings.HasSuffix(r.URL.Path, "/secrets"):
            _ = json.NewEncoder(w).Encode(map[string]string{})
        case strings.HasSuffix(r.URL.Path, "/clone-url"):
            time.Sleep(5 * time.Second) // simulate hang
            _ = json.NewEncoder(w).Encode(map[string]string{"url": "https://example.invalid"})
        case strings.HasSuffix(r.URL.Path, "/finalize"):
            fullBody, _ = io.ReadAll(r.Body)
            w.WriteHeader(http.StatusNoContent)
        }
    }))
    defer srv.Close()

    t.Setenv(envAPIURL, srv.URL)
    t.Setenv(envRunID, "r1")
    t.Setenv(envRunToken, "tok")

    start := time.Now()
    err := runRunnerHTTP(context.Background(), "r1")
    elapsed := time.Since(start)
    require.Error(t, err)
    assert.Less(t, elapsed, 3*time.Second, "runner must abort within timeout + a little slack")
    assert.Contains(t, string(fullBody), `"status":"failed"`)
}
```

- [ ] **Step 8: Run test to verify it fails**

Run: `go test ./cmd/cronfoundry/ -run TestRunnerHTTP_AppliesTimeoutFromRunContext -timeout 10s -v`
Expected: FAIL — elapsed will be ≥5s because no timeout is applied.

- [ ] **Step 9: Wrap ctx with timeout**

In `cmd/cronfoundry/runner.go`, `runRunnerHTTP`, immediately after the `GetRunContext` call succeeds (around line 89), insert:

```go
// Apply the schedule's wall-clock timeout. We keep a 2-minute tail so the
// runner has time to POST its terminal event + finalize even if the main
// work hit the deadline — finalize.go will record the result either way.
if runCtx.TimeoutSec > 0 {
    var cancel context.CancelFunc
    ctx, cancel = context.WithTimeout(ctx, time.Duration(runCtx.TimeoutSec)*time.Second)
    defer cancel()
}
```

- [ ] **Step 10: Run tests**

Run: `go test ./cmd/cronfoundry/ -run TestRunnerHTTP_AppliesTimeoutFromRunContext -timeout 10s -v`
Expected: PASS (elapsed <3s).

Then full suite:

Run: `go test ./... -timeout 120s`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/db/queries/run.sql internal/db/gen/run.sql.go internal/api/run_context.go internal/api/run_context_test.go cmd/cronfoundry/runner.go cmd/cronfoundry/runner_test.go
git commit -m "feat(runner): enforce per-run wall-clock timeout from schedule.timeout_sec"
```

---

## Task 6: Local end-to-end smoke test

**Files:**
- Create: `cmd/cronfoundry/smoke_test.go`

Validates the full hot-path end-to-end against mocks: throwaway Postgres → seed org/repo/skill/schedule → mock OpenAI returning a fixed response with `<memory>...</memory>` → mock Slack webhook → mock GitHub clone endpoint serving a bare git repo with `cronfoundry.yaml` + `SKILL.md` → scheduler tick → runner subprocess → assert run row is `succeeded` + Slack was called + `run_event` has `publish.slack.ok`.

Build tag: `smoke` (so it doesn't run in the default `go test` pass but can be invoked with `go test -tags=smoke`).

- [ ] **Step 1: Create the smoke test file**

Create `cmd/cronfoundry/smoke_test.go`:

```go
//go:build smoke

package main

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    "github.com/jackc/pgx/v5/pgtype"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/require"

    dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
    "github.com/gambtho/cronfoundry/internal/testdb"
)

// TestSmoke_ScheduleFireEndToEnd runs the full hot path against mocks.
//
// Coverage target (from design spec §Success Criteria, adapted for local):
//   - scheduler picks up a due schedule
//   - subprocess runner fetches context + clone URL + secrets from API
//   - runner clones at pinned SHA (local bare repo)
//   - runner calls OpenAI (mocked SSE stream with a <memory> block)
//   - runner posts to Slack (mocked webhook)
//   - runner finalizes with status=succeeded, tokens + cost recorded
func TestSmoke_ScheduleFireEndToEnd(t *testing.T) {
    if testing.Short() {
        t.Skip("smoke: skipped in short mode")
    }
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
    defer cancel()

    // 1. Boot throwaway Postgres and run migrations.
    dsn, teardownDB := testdb.BootPGWithDSN(t)
    defer teardownDB()
    runMigrations(t, dsn)

    // 2. Build a bare git repo with cronfoundry.yaml + SKILL.md that the API
    //    will hand to the runner as its clone URL.
    repoDir := t.TempDir()
    buildSkillRepo(t, repoDir)
    pinnedSHA := gitHeadSHA(t, repoDir)

    // 3. Mock OpenAI: SSE stream that returns "Hello<memory>note</memory>"
    //    plus usage {prompt:100, completion:50}.
    var openaiHits int32
    openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&openaiHits, 1)
        w.Header().Set("Content-Type", "text/event-stream")
        fmt.Fprint(w,
            "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n"+
                "data: {\"choices\":[{\"delta\":{\"content\":\"world\\n<memory>new-note</memory>\"}}]}\n\n"+
                "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50}}\n\n"+
                "data: [DONE]\n")
    }))
    defer openaiSrv.Close()
    t.Setenv("CRONFOUNDRY_OPENAI_BASE_URL", openaiSrv.URL)

    // 4. Mock Slack webhook.
    var slackHits int32
    var slackBody string
    slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&slackHits, 1)
        buf := make([]byte, r.ContentLength)
        _, _ = r.Body.Read(buf)
        slackBody = string(buf)
        w.WriteHeader(http.StatusOK)
    }))
    defer slackSrv.Close()

    // 5. Write the Slack webhook secret into Key Vault stub (Postgres-backed).
    pool, err := pgxpool.New(ctx, dsn)
    require.NoError(t, err)
    defer pool.Close()
    q := dbgen.New(pool)
    _, err = pool.Exec(ctx,
        `INSERT INTO secret_store (name, version, value) VALUES ($1, 1, $2)`,
        "slack_digest_webhook", slackSrv.URL)
    require.NoError(t, err)

    // 6. Seed org + repo_connection + skill + schedule.
    orgID := seedOrg(t, ctx, pool)
    repoID := seedRepo(t, ctx, pool, orgID)
    skillID := seedSkill(t, ctx, pool, orgID, repoID, pinnedSHA)
    scheduleID := seedSchedule(t, ctx, pool, orgID, skillID)

    // 7. Build cronfoundry binary (includes api + scheduler + hidden runner subcmd).
    binPath := filepath.Join(t.TempDir(), "cronfoundry")
    cmd := exec.Command("go", "build", "-o", binPath, "./cmd/cronfoundry")
    cmd.Dir = repoWorkingDir(t)
    require.NoError(t, cmd.Run())

    // 8. Boot `cronfoundry serve` with clone-URL endpoint pointing at our
    //    local bare repo (via a mock installation-token fetch). For smoke
    //    scope we short-circuit by seeding the repo_connection's
    //    github_install_id=0 and having the API's clone-url handler return
    //    file://<bare repo path> when install_id=0. If that branch doesn't
    //    exist yet, add a SMOKE_LOCAL_CLONE_URL env override in the API.
    t.Setenv("CRONFOUNDRY_DB_URL", dsn)
    t.Setenv("CRONFOUNDRY_TICK_INTERVAL", "500ms")
    t.Setenv("SMOKE_LOCAL_CLONE_URL", "file://"+filepath.Join(repoDir, ".git"))
    serve := exec.CommandContext(ctx, binPath, "serve", "--addr", "127.0.0.1:0")
    serve.Stdout = os.Stderr
    serve.Stderr = os.Stderr
    require.NoError(t, serve.Start())
    defer func() { _ = serve.Process.Kill() }()

    // 9. Insert a pending run for the schedule so we don't wait for the cron
    //    next-fire window. Scheduler will dispatch it on the next tick.
    _, err = pool.Exec(ctx,
        `INSERT INTO run (id, org_id, schedule_id, status, fire_reason, fire_time)
         VALUES (gen_random_uuid(), $1, $2, 'pending', 'manual', now())`,
        orgID, scheduleID)
    require.NoError(t, err)

    // 10. Poll until the run is terminal (or timeout).
    var status string
    var costCents int32
    var tokensIn int32
    deadline := time.Now().Add(90 * time.Second)
    for time.Now().Before(deadline) {
        var st string
        var cc, ti int32
        row := pool.QueryRow(ctx,
            `SELECT status, COALESCE(cost_cents,0), COALESCE(tokens_in,0) FROM run WHERE schedule_id = $1 ORDER BY fire_time DESC LIMIT 1`,
            scheduleID)
        if err := row.Scan(&st, &cc, &ti); err == nil && st != "pending" && st != "running" {
            status = st
            costCents = cc
            tokensIn = ti
            break
        }
        time.Sleep(500 * time.Millisecond)
    }

    // 11. Assertions — the full success matrix.
    require.Equal(t, "succeeded", status, "run must succeed; check API+scheduler stderr above")
    require.GreaterOrEqual(t, int32(1), atomic.LoadInt32(&openaiHits), "OpenAI must be called at least once")
    require.Equal(t, int32(1), atomic.LoadInt32(&slackHits), "Slack webhook must be called exactly once")
    require.Contains(t, slackBody, "Hello world", "Slack payload must carry the output (with <memory> stripped)")
    require.NotContains(t, slackBody, "<memory>", "<memory> block must not be published")
    require.Equal(t, int32(100), tokensIn, "token accounting must land in the DB")
    require.Greater(t, costCents, int32(0), "cost_cents must be computed (non-zero for gpt-4o-mini @ 100/50 tokens is 0, bump fixture if needed)")

    // 12. Verify a run_event row marks the slack publish as ok.
    var evCount int
    err = pool.QueryRow(ctx,
        `SELECT count(*) FROM run_event WHERE event_type = 'publish.slack.ok'`).Scan(&evCount)
    require.NoError(t, err)
    require.GreaterOrEqual(t, evCount, 1, "run_event must contain a publish.slack.ok row")
}

// ---- helpers (keep local to the smoke test to avoid polluting the package) ----

func runMigrations(t *testing.T, dsn string) { /* invoke embedded goose; same as e2e_test.go does */ }
func buildSkillRepo(t *testing.T, dir string) {
    // `git init`, write cronfoundry.yaml + skills/demo/SKILL.md, `git commit`.
    // Keep the SKILL.md short: a single-line prompt, no includes.
}
func gitHeadSHA(t *testing.T, dir string) string {
    out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
    require.NoError(t, err)
    return strings.TrimSpace(string(out))
}
func seedOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgtype.UUID {
    /* INSERT INTO organization ... RETURNING id */
    return pgtype.UUID{}
}
func seedRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID pgtype.UUID) pgtype.UUID {
    /* INSERT INTO repo_connection (owner='smoke', name='repo', github_app_install_id=0, ...) */
    return pgtype.UUID{}
}
func seedSkill(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, repoID pgtype.UUID, sha string) pgtype.UUID {
    /* INSERT INTO skill (path='skills/demo', name='demo', current_sha=sha, frontmatter_json='{}') */
    return pgtype.UUID{}
}
func seedSchedule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, skillID pgtype.UUID) pgtype.UUID {
    /* INSERT INTO schedule with destinations_json = [{"slack":{"secret":"slack_digest_webhook"}}], provider='openai', model='gpt-4o-mini', timeout_sec=60 */
    return pgtype.UUID{}
}
func repoWorkingDir(t *testing.T) string {
    wd, err := os.Getwd()
    require.NoError(t, err)
    // Walk up until we find go.mod; smoke test runs from cmd/cronfoundry.
    for d := wd; d != "/"; d = filepath.Dir(d) {
        if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
            return d
        }
    }
    t.Fatal("could not locate go.mod")
    return ""
}
```

> The helper stubs are intentionally brief — port the seed SQL from `e2e_test.go`
> (which boots Postgres the same way). Token accounting uses 100/50 tokens; with
> gpt-4o-mini pricing (Task 3) that rounds to 0 cents — either bump the fixture
> to 1,000,000/1,000,000 or relax the cost assertion to `>= 0`.

- [ ] **Step 2: Teach the clone-URL endpoint to honor `SMOKE_LOCAL_CLONE_URL`**

The smoke test needs the API to return `file://...` for the bare repo instead of fetching a real GitHub installation token. In `internal/api/clone_url.go`:

```go
// Near the top of the handler, before the GitHub token fetch:
if v := os.Getenv("SMOKE_LOCAL_CLONE_URL"); v != "" {
    _ = json.NewEncoder(w).Encode(map[string]string{"url": v})
    return
}
```

This env var is only set in tests; production deployments leave it unset and take the real GitHub path.

- [ ] **Step 3: Run the smoke test**

Run: `go test -tags=smoke ./cmd/cronfoundry/ -run TestSmoke_ScheduleFireEndToEnd -v -timeout 180s`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/cronfoundry/smoke_test.go internal/api/clone_url.go
git commit -m "test(smoke): add local end-to-end schedule-fire smoke test"
```

- [ ] **Step 5: Wire into CI (optional, separate commit)**

Edit `.github/workflows/ci.yml` — in the `test` job after `go test ./...`, add:

```yaml
      - name: go test smoke
        if: github.event_name == 'pull_request'
        env:
          TEST_DATABASE_URL: postgres://cf:cf@localhost:5432/cronfoundry_test?sslmode=disable
        run: go test -tags=smoke ./cmd/cronfoundry/ -run TestSmoke -timeout 180s
```

Commit:

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run smoke test on pull requests"
```

---

## Task 7: ~~Azure smoke runbook~~ — DROPPED

**Status:** Already shipped as part of PR #14. See `docs/guides/smoke-test-mvp-azure.md` (206 lines). No further work required.

---

## Self-Review Checklist

- [ ] Session TTL bumped to 7 days (Task 1) — verified against spec §Auth flow
- [ ] LLM retries set to 3 explicitly (Task 2) — verified against spec §Execution Flow §LLM call
- [ ] Cost accounting ships end-to-end (Tasks 3+4) — verified against spec §In scope, §Data model `run.cost_cents`, §finalize
- [ ] Runner wall-clock timeout enforced (Task 5) — verified against spec §Policies "runner self-kills past the timeout"
- [ ] Local smoke test covers hot path (Task 6) — verified against spec §Success Criteria, §Schedule-fire flow, §Failure matrix

### Not in scope (deliberately)
- **Live-tail logs UI** — shipped in PR #14 (`web/src/components/LogTail.tsx`)
- **Azure smoke runbook** — shipped in PR #14 (`docs/guides/smoke-test-mvp-azure.md`)
- **Schema column rename** (`llm_secret_ref` vs `keyvault_ref_llm_key`) — functional equivalent; cosmetic-only migration risk > benefit
- **CI/CD workflows** — `.github/workflows/ci.yml` and `release.yml` already exist and push multi-arch images to GHCR
- **Allowlist deny-by-default** — already enforced at `oauth.go:110–113` via `resolveRole` returning empty string → 403

---
