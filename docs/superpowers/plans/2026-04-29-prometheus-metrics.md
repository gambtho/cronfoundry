# Prometheus Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose a Prometheus `/metrics` endpoint with 6 business counters + 1 histogram covering run starts, finishes, duration, LLM tokens/cost, and destination publishes — plus the default Go runtime collectors that ship with `promhttp`.

**Architecture:** A new `internal/metrics` package declares all metrics as package-level `*prometheus.CounterVec` / `*HistogramVec` vars, registered automatically via `promauto`. Producers (scheduler, finalize handler, publishers) call `metrics.RunsStarted.WithLabelValues(...).Inc()` etc. The webapi mounts `metrics.Handler()` at `/metrics` outside any auth/rate-limit middleware. A `Disabled` package var flips the handler to 404 — producer increments still record into the registry; only the scrape endpoint is gated.

**Tech Stack:** Go (`github.com/prometheus/client_golang`, `net/http`), `stretchr/testify`. One new direct dep with one transitive (`prometheus/common`).

**Spec:** [`docs/superpowers/specs/2026-04-29-prometheus-metrics-design.md`](../specs/2026-04-29-prometheus-metrics-design.md)

## File Map

- **Create** `internal/metrics/metrics.go` — package-level metric vars + `Handler()` + `Disabled` flag.
- **Create** `internal/metrics/metrics_test.go` — registration / scrape format / disabled / histogram bucket tests.
- **Modify** `internal/webapi/server.go` — `mux.Handle("GET /metrics", metrics.Handler())` outside any middleware chain.
- **Modify** `internal/scheduler/tick.go` — increment `RunsStarted` after each successful dispatch.
- **Modify** `internal/api/finalize.go` — increment `RunsFinished`, observe `RunDuration`, increment `LLMTokens`+`LLMCost` on finalize.
- **Modify** `internal/publish/dispatcher.go` (or each publisher) — increment `DestPublish{type,result}` per publish attempt.
- **Modify** `cmd/cronfoundry/serve.go` — read `CRONFOUNDRY_METRICS_DISABLED` and set `metrics.Disabled` at startup.
- **Modify** `go.mod`, `go.sum` — add `prometheus/client_golang`.
- **Modify** `docs/guides/observability.md` — document endpoint + metric names + sample queries.
- **Modify** `README.md` — add `/metrics` to operator endpoints.

---

### Task 1: Create the `metrics` package with all six metrics + `Handler`

**Files:**
- Create: `internal/metrics/metrics.go`
- Create: `internal/metrics/metrics_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
cd /home/tng/workspace/cronfoundry/.claude/worktrees/spec-metrics
go get github.com/prometheus/client_golang@latest
```

This adds the dep to `go.mod`. We'll keep it (and not lose it to `go mod tidy`) by importing it from `metrics.go` in the same task.

- [ ] **Step 2: Write the failing tests**

Create `internal/metrics/metrics_test.go`:

```go
package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsRegistered(t *testing.T) {
	want := []string{
		"cronfoundry_runs_started_total",
		"cronfoundry_runs_finished_total",
		"cronfoundry_run_duration_seconds",
		"cronfoundry_llm_tokens_total",
		"cronfoundry_llm_cost_usd_total",
		"cronfoundry_destination_publish_total",
	}
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	got := map[string]bool{}
	for _, mf := range mfs {
		got[mf.GetName()] = true
	}
	for _, name := range want {
		assert.True(t, got[name], "missing metric %s", name)
	}
}

func TestRunsStarted_IncrementsAndScrapes(t *testing.T) {
	RunsStarted.WithLabelValues("test-schedule").Inc()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `cronfoundry_runs_started_total{schedule="test-schedule"}`)
}

func TestHandler_PrometheusFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	ct := rec.Header().Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/plain"), "got Content-Type %q", ct)
	assert.Contains(t, rec.Body.String(), "# HELP cronfoundry_runs_started_total")
	assert.Contains(t, rec.Body.String(), "# TYPE cronfoundry_runs_started_total counter")
}

func TestHandler_Disabled(t *testing.T) {
	prev := Disabled
	Disabled = true
	defer func() { Disabled = prev }()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHistogram_HasExpectedBuckets(t *testing.T) {
	RunDuration.WithLabelValues("succeeded").Observe(0.1)
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	var h *dto.Histogram
	for _, mf := range mfs {
		if mf.GetName() == "cronfoundry_run_duration_seconds" {
			for _, m := range mf.GetMetric() {
				h = m.GetHistogram()
				break
			}
			break
		}
	}
	require.NotNil(t, h, "histogram not found")
	// 15 explicit buckets (Prometheus default 11 + four long-tail).
	assert.Len(t, h.GetBucket(), 15)
}
```

You'll need `github.com/prometheus/client_model/go` — `go get` it before running:

```bash
go get github.com/prometheus/client_model/go@latest
```

- [ ] **Step 3: Run, expect compile errors**

```bash
go test ./internal/metrics/ -v
```

Expected: `no Go files in ...` or `undefined: RunsStarted, RunsFinished, RunDuration, LLMTokens, LLMCost, DestPublish, Handler, Disabled`.

- [ ] **Step 4: Implement the package**

Create `internal/metrics/metrics.go`:

```go
// Package metrics defines all Prometheus metrics emitted by cronfoundry
// and exposes a /metrics handler. Producers import this package and call
// the package-level vars directly, e.g. metrics.RunsStarted.WithLabelValues(...).Inc().
//
// Set Disabled=true to short-circuit the handler (returns 404). Increments
// on disabled metrics are still safe — Prometheus client lib is allocation-free
// for label-only Inc() — so toggling Disabled at runtime requires no further
// changes to producer code.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Disabled, when true, makes Handler() return 404. Set at startup from
// CRONFOUNDRY_METRICS_DISABLED. Default false.
var Disabled bool

// Histogram buckets for run_duration_seconds: Prometheus DefBuckets (0.005s … 10s)
// extended with 30/60/300/600 to capture long-running LLM calls.
var runDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 600,
}

const namespace = "cronfoundry"

// RunsStarted counts run dispatches by the scheduler, partitioned by schedule name.
var RunsStarted = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "runs_started_total",
	Help:      "Total runs dispatched by the scheduler.",
}, []string{"schedule"})

// RunsFinished counts terminal run states.
var RunsFinished = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "runs_finished_total",
	Help:      "Total runs that reached a terminal state, partitioned by status (succeeded|partial_failure|failed).",
}, []string{"status"})

// RunDuration observes wall-clock run time, partitioned by terminal status.
var RunDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: namespace,
	Name:      "run_duration_seconds",
	Help:      "Wall-clock run duration from dispatch to finalize, partitioned by status.",
	Buckets:   runDurationBuckets,
}, []string{"status"})

// LLMTokens counts tokens consumed, partitioned by provider and direction (input|output).
var LLMTokens = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "llm_tokens_total",
	Help:      "Total LLM tokens, partitioned by provider and kind (input|output).",
}, []string{"provider", "kind"})

// LLMCost accumulates estimated USD cost, partitioned by provider.
var LLMCost = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "llm_cost_usd_total",
	Help:      "Estimated LLM cost in USD, partitioned by provider.",
}, []string{"provider"})

// DestPublish counts publish attempts per destination type and outcome.
var DestPublish = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "destination_publish_total",
	Help:      "Total destination publish attempts, partitioned by type (slack|discord|teams|github_issue|http|email) and result (ok|error).",
}, []string{"type", "result"})

// Handler returns the /metrics HTTP handler. When Disabled is true, returns 404.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Disabled {
			http.NotFound(w, r)
			return
		}
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
}
```

- [ ] **Step 5: Run, expect PASS**

```bash
go mod tidy
go test ./internal/metrics/ -v
go vet ./...
go build ./...
```

Expected: 5 PASS, vet clean, build clean.

If `go mod tidy` strips the `client_model/go` dep because it's only used in tests, that's fine — go.mod will keep the test-only entry.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/metrics/metrics.go internal/metrics/metrics_test.go
git commit -m "metrics: add Prometheus metrics package with 6 business metrics"
```

---

### Task 2: Mount `/metrics` in the webapi mux

**Files:**
- Modify: `internal/webapi/server.go`
- Test: `internal/webapi/metrics_route_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/webapi/metrics_route_test.go`:

```go
package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestMetricsRouteServed(t *testing.T) {
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey: make([]byte, 32),
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "cronfoundry_runs_started_total")
}
```

- [ ] **Step 2: Run, expect 404**

```bash
go test ./internal/webapi/ -run TestMetricsRouteServed -v
```

Expected: 404 (route not registered).

- [ ] **Step 3: Wire the handler**

In `internal/webapi/server.go`, find a stable spot in `RegisterRoutes` (e.g., right before `/webhook/github`). Add:

```go
mux.Handle("GET /metrics", metrics.Handler())
```

Add the import to the import block of `internal/webapi/server.go`:

```go
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 4: Run, expect PASS**

```bash
go test ./internal/webapi/ -run TestMetricsRouteServed -v
go test -short ./internal/webapi/ -v
go vet ./...
```

Expected: PASS, no regressions, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/server.go internal/webapi/metrics_route_test.go
git commit -m "metrics: mount /metrics handler in webapi mux"
```

---

### Task 3: Increment `RunsStarted` from the scheduler

**Files:**
- Modify: `internal/scheduler/tick.go`
- Test: `internal/scheduler/tick_test.go`

- [ ] **Step 1: Find the dispatch site**

```bash
grep -n "dispatch\|enqueue\|InsertRun\|run_id" internal/scheduler/tick.go | head -20
```

Identify the line where a new run row is created and the run is handed off (around line 250-360 — look for where `pendingRunID` becomes a started run, or where `cloud.Dispatch`/`subprocessRunner` is called).

- [ ] **Step 2: Add the increment**

In `internal/scheduler/tick.go`, immediately after the dispatch succeeds (the line where the runner is launched and the row is set to `running`), add:

```go
metrics.RunsStarted.WithLabelValues(scheduleName).Inc()
```

`scheduleName` should already be in scope at the dispatch site (look for `sched.Name` or similar). If only the schedule UUID is in scope at that point, walk up to where the schedule name is queried and pass it down.

Add the import to `internal/scheduler/tick.go`:

```go
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 3: Add a unit test**

In `internal/scheduler/tick_test.go`, append:

```go
func TestDispatch_IncrementsRunsStarted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	// Take a snapshot of the metric before, run the dispatch path, snapshot after.
	before := readCounter(t, metrics.RunsStarted, "test-schedule-name")

	// Reuse the existing test that already exercises a successful dispatch
	// — find one (e.g., TestTick_DispatchesPending or similar) and either
	// extract its body into a helper, or call its body inline. The schedule
	// it dispatches must have name "test-schedule-name" (rename the seed
	// data to match if needed).
	dispatchOneTestRun(t)  // helper extracted from existing test

	after := readCounter(t, metrics.RunsStarted, "test-schedule-name")
	assert.Equal(t, before+1, after)
}

func readCounter(t *testing.T, vec *prometheus.CounterVec, labelValues ...string) float64 {
	t.Helper()
	m, err := vec.GetMetricWithLabelValues(labelValues...)
	require.NoError(t, err)
	var pb dto.Metric
	require.NoError(t, m.Write(&pb))
	return pb.GetCounter().GetValue()
}
```

If `dispatchOneTestRun` doesn't exist as a helper, look at the existing `TestTick_*` tests and either extract a helper or merge the assertion into an existing test that already exercises dispatch.

> **Note:** if extracting the dispatch path into a reusable helper is more refactoring than is justified for a single counter assertion, just append the metric assertion at the end of the existing TestTick_DispatchesPending test (or whichever test exercises the green dispatch path). Less elegant but more YAGNI-aligned for a smoke-level metric check.

Imports needed in tick_test.go:
```go
"github.com/prometheus/client_golang/prometheus"
dto "github.com/prometheus/client_model/go"
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 4: Verify**

```bash
go test ./internal/scheduler/ -v 2>&1 | tail -10
go vet ./...
```

Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/tick.go internal/scheduler/tick_test.go
git commit -m "metrics: increment runs_started_total on schedule dispatch"
```

---

### Task 4: Increment finalize-time metrics from `internal/api/finalize.go`

**Files:**
- Modify: `internal/api/finalize.go`
- Test: `internal/api/finalize_test.go`

- [ ] **Step 1: Identify available data in finalize**

The finalize body already has `Status`, `DurationMs`, `TokensIn`, `TokensOut`, `CostCents`. The provider is NOT in the body — it's stored on the run record. Read `internal/api/finalize.go` end-to-end to find where the run record is fetched within the same handler. If the handler currently doesn't fetch the run, look for a query like `GetRun` or `GetRunForFinalize` in `internal/db/gen/run.sql.go` — if present, use it.

If no fetch exists today and adding one is intrusive, simplify v1 by emitting LLM metrics WITHOUT the provider label (just `cronfoundry_llm_tokens_total{kind="input"}`). Spec calls for the provider label — flag this as a concern in your report; we can refine after the runner pushes provider in the body in a follow-up.

For this plan, assume the provider IS fetchable. If it isn't:
- Emit LLMTokens / LLMCost with `provider="unknown"` and note the follow-up.

- [ ] **Step 2: Write a failing test**

Append to `internal/api/finalize_test.go`. There's an existing happy-path finalize test (look for `TestFinalize_Succeeded` or similar). Either extend it with metric assertions or copy its setup into a new test:

```go
func TestFinalize_IncrementsMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Postgres")
	}
	// Reuse setup from TestFinalize_Succeeded — set up a run row, build the
	// finalize handler with auth bypass, POST a finalize body.
	beforeFinished := readCounter(t, metrics.RunsFinished, "succeeded")
	beforeTokensIn := readCounter(t, metrics.LLMTokens, "openai", "input")

	// ... call finalize with status=succeeded, tokens_in=100, tokens_out=200, cost_cents=5 ...

	afterFinished := readCounter(t, metrics.RunsFinished, "succeeded")
	afterTokensIn := readCounter(t, metrics.LLMTokens, "openai", "input")
	assert.Equal(t, beforeFinished+1, afterFinished)
	assert.Equal(t, beforeTokensIn+100, afterTokensIn)
}

func readCounter(t *testing.T, vec *prometheus.CounterVec, labelValues ...string) float64 {
	t.Helper()
	m, err := vec.GetMetricWithLabelValues(labelValues...)
	require.NoError(t, err)
	var pb dto.Metric
	require.NoError(t, m.Write(&pb))
	return pb.GetCounter().GetValue()
}
```

If a `readCounter` helper already exists in this package's test suite from Task 3 (it doesn't — Task 3 puts it in scheduler), it's fine to duplicate. The helper is 5 LOC; DRY across packages isn't worth a new shared test util for this.

Required imports:
```go
"github.com/prometheus/client_golang/prometheus"
dto "github.com/prometheus/client_model/go"
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 3: Implement the increments**

Find the spot in `finalize.go` AFTER the row is updated successfully (i.e., commit succeeded, before responding 204). Add:

```go
metrics.RunsFinished.WithLabelValues(body.Status).Inc()
if body.DurationMs != nil {
	metrics.RunDuration.WithLabelValues(body.Status).Observe(float64(*body.DurationMs) / 1000.0)
}
if provider != "" {
	if body.TokensIn != nil && *body.TokensIn > 0 {
		metrics.LLMTokens.WithLabelValues(provider, "input").Add(float64(*body.TokensIn))
	}
	if body.TokensOut != nil && *body.TokensOut > 0 {
		metrics.LLMTokens.WithLabelValues(provider, "output").Add(float64(*body.TokensOut))
	}
	if body.CostCents != nil && *body.CostCents > 0 {
		metrics.LLMCost.WithLabelValues(provider).Add(float64(*body.CostCents) / 100.0)
	}
}
```

Where `provider` comes from the run record fetch you did earlier. If you couldn't get provider, set `provider := "unknown"` and emit the metrics anyway (Prometheus accepts non-empty label values).

Add the import:
```go
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 4: Verify**

```bash
go test ./internal/api/ 2>&1 | tail -10
go vet ./...
```

Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/api/finalize.go internal/api/finalize_test.go
git commit -m "metrics: emit runs_finished, run_duration, llm_tokens, llm_cost on finalize"
```

---

### Task 5: Increment `DestPublish` per destination publish

**Files:**
- Modify: `internal/publish/dispatcher.go`
- Test: `internal/publish/dispatcher_test.go`

- [ ] **Step 1: Read the dispatcher**

Look at `internal/publish/dispatcher.go`:

```bash
cat internal/publish/dispatcher.go
```

Identify the spot where each `Result` is written into `results[i]`. That's the natural increment point — every destination, regardless of success or skip, runs through this path.

- [ ] **Step 2: Write failing test**

Append to `internal/publish/dispatcher_test.go`:

```go
func TestDispatch_IncrementsDestPublishMetric(t *testing.T) {
	beforeOk := readCounter(t, metrics.DestPublish, "slack", "ok")
	beforeErr := readCounter(t, metrics.DestPublish, "slack", "error")

	// Reuse existing test setup that dispatches a single slack destination.
	// If TestDispatcher_DispatchesSlack_OK exists, copy the setup here and
	// invoke d.Dispatch with a single slack dest where the slack publisher
	// returns OK. Then add a second case where it returns an error.

	// ... happy path: one slack OK ...
	// ... error path: one slack error ...

	afterOk := readCounter(t, metrics.DestPublish, "slack", "ok")
	afterErr := readCounter(t, metrics.DestPublish, "slack", "error")
	assert.Equal(t, beforeOk+1, afterOk)
	assert.Equal(t, beforeErr+1, afterErr)
}

func readCounter(t *testing.T, vec *prometheus.CounterVec, labelValues ...string) float64 {
	t.Helper()
	m, err := vec.GetMetricWithLabelValues(labelValues...)
	require.NoError(t, err)
	var pb dto.Metric
	require.NoError(t, m.Write(&pb))
	return pb.GetCounter().GetValue()
}
```

If a `readCounter` helper is already in this package's test files, don't duplicate — reuse.

- [ ] **Step 3: Implement the increment**

In `internal/publish/dispatcher.go`'s `publishOne` (or wherever a `Result` is finalized), at the end of the function (or in the goroutine after `results[i] = ...`), add:

```go
result := r // the *Result built up to this point
label := "ok"
if !result.OK {
	label = "error"
}
if !result.Skipped {
	metrics.DestPublish.WithLabelValues(string(result.Type), label).Inc()
}
```

Skipped results aren't an attempt — don't count them. Adjust to fit the actual data flow; the spec is "publish attempts and their outcomes."

Add the import:
```go
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 4: Verify**

```bash
go test ./internal/publish/ -v 2>&1 | tail -10
go vet ./...
```

Expected: PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/dispatcher.go internal/publish/dispatcher_test.go
git commit -m "metrics: increment destination_publish_total per publish attempt"
```

---

### Task 6: Wire `CRONFOUNDRY_METRICS_DISABLED` env var in serve.go

**Files:**
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Add the env-var constant**

Near the existing `env*` block in `cmd/cronfoundry/serve.go`:

```go
envMetricsDisabled = "CRONFOUNDRY_METRICS_DISABLED"
```

- [ ] **Step 2: Read and apply**

Near startup (right before `webapi.RegisterRoutes(...)`), add:

```go
metrics.Disabled = envBool(envMetricsDisabled)
```

(Reuse the `envBool` helper — added in earlier worktrees, but since this branch was off main it doesn't exist here yet. If absent, add this minimal version near the end of the file:

```go
func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}
```

Add `"strings"` to the import block if not present.)

Add the metrics import:
```go
"github.com/gambtho/cronfoundry/internal/metrics"
```

- [ ] **Step 3: Verify**

```bash
go build ./...
go vet ./...
go test -short ./...
```

All clean.

- [ ] **Step 4: Commit**

```bash
git add cmd/cronfoundry/serve.go
git commit -m "metrics: read CRONFOUNDRY_METRICS_DISABLED at startup"
```

---

### Task 7: Document the endpoint and metrics

**Files:**
- Modify: `docs/guides/observability.md`
- Modify: `README.md`

- [ ] **Step 1: Read current observability doc**

```bash
cat docs/guides/observability.md
```

Decide where to insert (top, dedicated section, etc.). Keep the existing logs/audit content intact.

- [ ] **Step 2: Add a metrics section**

Append (or insert appropriately) to `docs/guides/observability.md`:

````markdown
## Metrics — Prometheus `/metrics` endpoint

The serve container exposes a Prometheus scrape endpoint at `/metrics`.
It is unauthenticated and emits the standard Prometheus text format.

### Configuring scrape

Azure Monitor managed Prometheus scrapes any HTTP endpoint serving the
Prometheus exposition format; point a `PodMonitor` or `ScrapeConfig` at
`https://<your-fqdn>/metrics`. Scrape interval 30-60s is plenty.

For self-managed Prometheus, the scrape config is:

```yaml
scrape_configs:
  - job_name: cronfoundry
    metrics_path: /metrics
    static_configs:
      - targets: ['cronfoundry.example.com']
```

### Metrics emitted

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `cronfoundry_runs_started_total` | counter | `schedule` | Run dispatches by the scheduler |
| `cronfoundry_runs_finished_total` | counter | `status` | Terminal run states (`succeeded`, `partial_failure`, `failed`) |
| `cronfoundry_run_duration_seconds` | histogram | `status` | Wall-clock dispatch→finalize time |
| `cronfoundry_llm_tokens_total` | counter | `provider`, `kind` | LLM tokens (`input` or `output`) |
| `cronfoundry_llm_cost_usd_total` | counter | `provider` | Estimated USD cost |
| `cronfoundry_destination_publish_total` | counter | `type`, `result` | Publish attempts (`ok` or `error`) |

Plus standard Go runtime metrics (`go_goroutines`, `go_gc_duration_seconds`,
`process_resident_memory_bytes`, etc.).

### Example queries

7-day run success rate:
```
sum(rate(cronfoundry_runs_finished_total{status="succeeded"}[7d]))
  / sum(rate(cronfoundry_runs_finished_total[7d]))
```

P95 run duration:
```
histogram_quantile(0.95, sum by (le) (rate(cronfoundry_run_duration_seconds_bucket[1h])))
```

Daily LLM cost by provider:
```
sum by (provider) (increase(cronfoundry_llm_cost_usd_total[1d]))
```

Destination failure rate (last hour):
```
sum by (type) (rate(cronfoundry_destination_publish_total{result="error"}[1h]))
  / sum by (type) (rate(cronfoundry_destination_publish_total[1h]))
```

### Disabling

Set `CRONFOUNDRY_METRICS_DISABLED=true` to make `/metrics` return 404.
The internal counters still increment (cheap), so the toggle is reversible
without a restart-then-restart hop.
````

- [ ] **Step 3: README operator-endpoints update**

In `README.md`, find the `### Operator endpoints` section. Add:

```markdown
- `GET /metrics` — Prometheus text-format scrape endpoint (see
  [`docs/guides/observability.md`](docs/guides/observability.md))
```

- [ ] **Step 4: Commit**

```bash
git add docs/guides/observability.md README.md
git commit -m "metrics: document /metrics endpoint and example PromQL queries"
```

---

### Task 8: Open the PR

- [ ] **Step 1: Push**

```bash
git push -u origin worktree-spec-metrics
```

- [ ] **Step 2: Open PR**

```bash
gh pr create --title "feat(observability): Prometheus /metrics endpoint" --body "$(cat <<'EOF'
## Summary

Implements [the Prometheus metrics design](docs/superpowers/specs/2026-04-29-prometheus-metrics-design.md), closing PRD FR-8.4 (release-readiness review item #4).

- New `internal/metrics` package declares 6 business metrics + 1 histogram (run starts, finishes, duration, LLM tokens/cost, destination publishes)
- `/metrics` endpoint mounted in `webapi.RegisterRoutes`, outside auth/rate-limit/CSRF middleware (Prometheus convention)
- Producers: scheduler dispatch site, finalize handler, publish dispatcher
- `CRONFOUNDRY_METRICS_DISABLED` kill switch (returns 404)
- Default Go runtime collectors (goroutines, GC, mem) ship for free with `promhttp`
- New direct dep: `github.com/prometheus/client_golang`

## Operator note

Scrape with Azure Monitor managed Prometheus, vanilla Prometheus, or any compatible scraper. No auth — metrics are low-sensitivity counters; private-VNet deploys are trivially safe, public-ingress deploys leak only an activity fingerprint.

## Test plan

- [x] `go test ./internal/metrics/...` green (registration, scrape format, histogram buckets, disabled-route)
- [x] Producer unit tests assert metric increments after dispatch / finalize / publish
- [x] `go vet ./...` clean
- [ ] Manual: `curl https://<fqdn>/metrics` after a successful run; verify `cronfoundry_runs_started_total` and `cronfoundry_runs_finished_total{status="succeeded"}` are ≥ 1
- [ ] Manual: set `CRONFOUNDRY_METRICS_DISABLED=true`; `/metrics` returns 404

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Report PR URL**

---

## Self-review

**Spec coverage:**

- 6 business metrics declared in one place → Task 1
- Histogram with custom buckets → Task 1
- `Handler()` + `Disabled` flag → Task 1
- `/metrics` mounted on webapi mux → Task 2
- Producer wiring (scheduler, finalize, publishers) → Tasks 3, 4, 5
- Env-var kill switch wiring → Task 6
- Docs (observability guide + README) → Task 7

**Placeholder scan:** Two judgment-call notes left intentionally:
- Task 3 step 3 — extracting a dispatch helper vs. appending to existing test, with explicit guidance to pick the cheaper option.
- Task 4 step 1 — provider lookup; with a fall-through to `provider="unknown"` if the lookup is intrusive, plus a note to flag follow-up work.
Both are tagged "if X, do Y; otherwise do Z" — implementer doesn't need to invent. Acceptable.

**Type consistency:**

- `metrics.RunsStarted`, `RunsFinished`, `RunDuration`, `LLMTokens`, `LLMCost`, `DestPublish`, `Handler`, `Disabled` — used identically across Tasks 1, 2, 3, 4, 5, 6.
- Label keys consistent: `schedule`, `status`, `provider`, `kind`, `type`, `result`.
- `readCounter` test helper has the same signature in Tasks 3, 4, 5 (acceptably duplicated across packages — small enough not to extract).

**One spec/plan ambiguity caught and resolved inline:** the spec declares `provider` as a label on `LLMTokens`/`LLMCost`. The plan's Task 4 explicitly handles the case where provider isn't readily available in finalize today, with `provider="unknown"` as a safe fallback and a follow-up note. Spec stays satisfied; implementation stays YAGNI.
