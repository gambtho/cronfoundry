# Prometheus Metrics — Technical Design

**Status:** Proposed
**Date:** 2026-04-29
**Author:** gambtho
**Companion:** Release-readiness review (item #4)

## Background

PRD `2026-04-19-cronfoundry-prd.md` FR-8.4 commits to "Run-level metrics emitted as
OpenTelemetry counters/gauges suitable for Azure Monitor scraping." Today
the codebase has zero metrics — `grep -rn "otel\|opentelemetry\|prometheus\|metrics\."`
returns nothing in production code. Without metrics, the PRD success
criterion "Run success rate > 99% over a rolling 7 days" cannot be
verified, and operators cannot alert on degradation.

This design ships a `/metrics` endpoint exposing a focused set of
Prometheus counters and one histogram, scraped by Azure Monitor managed
Prometheus (which natively supports the Prometheus text format) or any
standard Prometheus server. OpenTelemetry was the original PRD wording;
Prometheus is functionally equivalent for our needs and ~10x simpler to
wire — Azure Monitor's managed Prom collector scrapes a `/metrics` HTTP
endpoint identically.

## Goals

1. Operators can answer "is the system healthy" via Prometheus queries.
2. Run success rate, latency P95, LLM cost, and destination publish
   success can all be derived from the metric set.
3. Adding new metrics is a one-place change (the `metrics` package).
4. No measurable hot-path regression — counter increments are cheap.

## Non-goals

- Distributed tracing (spans). Deferred — needs OTel; out of scope for
  this design.
- Custom dashboards / alerts. The metrics are exposed; downstream
  dashboarding is a separate operator concern.
- Cardinality protection beyond label discipline. Label values are
  bounded by design (status enums, provider names, etc.) — no
  user-supplied labels.
- Token-based metric scrape auth. Out of scope; metrics are
  low-sensitivity counters.

## Endpoint

```http
GET /metrics
```

- Public, unauthenticated. Standard Prometheus scrape convention.
- Returns Prometheus text-format exposition (Content-Type set by
  promhttp).
- NOT rate-limited (Prometheus scrapes every 15-60s; rate limiter would
  occasionally drop scrapes and create gaps).
- NOT CSRF-protected (it's a GET).
- Wired in `webapi.RegisterRoutes` outside the `session()` and
  `adminOnly()` chains.

## Metrics

| Metric | Type | Labels | Source |
|---|---|---|---|
| `cronfoundry_runs_started_total` | counter | `schedule` (schedule name) | `internal/scheduler/tick.go` when a run is dispatched |
| `cronfoundry_runs_finished_total` | counter | `status` (succeeded \| partial_failure \| failed) | `internal/api` finalize handler when run completes |
| `cronfoundry_run_duration_seconds` | histogram | `status` | finalize handler — `started_at` to `finished_at` delta |
| `cronfoundry_llm_tokens_total` | counter | `provider` (openai\|anthropic\|azure\|copilot\|openrouter\|unknown), `kind` (input\|output) | finalize handler from run summary |
| `cronfoundry_llm_cost_usd_total` | counter | `provider` | finalize handler |
| `cronfoundry_destination_publish_total` | counter | `type` (slack\|discord\|teams\|github_issue\|http\|email), `result` (ok\|error) | per-destination publisher |

Plus the default Go runtime collectors (`go_goroutines`,
`go_gc_duration_seconds`, `process_resident_memory_bytes`, etc.) that
ship with `promhttp.HandlerFor(prometheus.DefaultGatherer, ...)`.

> **Provider label note.** The finalize handler currently emits
> `provider="unknown"` for every run because the provider isn't on the
> finalize row today. A follow-up will plumb provider through (either on
> the row or in the finalize body) and then the values listed above
> become the true emitted set. Until then, expect `provider="unknown"`
> for all LLM series.

### Cardinality bounds

- `schedule` — bounded by the number of schedules in the manifest (small
  for any real deploy; documented as "if you have 10K+ schedules, this
  metric will create 10K+ series — acceptable trade-off for visibility").
- `status` — 3 values total.
- `provider` — fixed enum of 6 (5 named + `unknown`).
- `kind` — 2 values.
- `type` — fixed enum of 6.
- `result` — 2 values.

Worst case for a 100-schedule deploy: ~100 (started) + 3 (finished
status) + 30 (duration histogram buckets × statuses) + 12 (6 providers
× 2 kinds) + 6 (cost per provider) + 12 (6 types × 2 results) ≈ 163
series, plus ~50 from Go runtime. Well within Prometheus comfort.

### Histogram buckets

`run_duration_seconds` uses Prometheus default buckets
(`prometheus.DefBuckets`: 0.005s … 10s) PLUS four larger buckets for
long-running LLM calls: `30, 60, 300, 600`. Final list:

```text
0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 600
```

Skews a bit late but most cronfoundry runs land between 1-60s; the long
tail matters for catching stuck/slow runs.

## Component layout

A new package: `internal/metrics`. Not under `internal/webapi` because
the runner (`cmd/runner`) and the scheduler (`internal/scheduler`) both
emit metrics — webapi is just one consumer.

```text
internal/metrics/
├── metrics.go        // declares all metrics as package-level vars; init() registers them
└── metrics_test.go   // smoke tests verifying registration + sample expositions
```

```go
// metrics.go (sketch)
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    RunsStarted = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "cronfoundry",
            Name:      "runs_started_total",
            Help:      "Total runs dispatched by the scheduler, partitioned by schedule name.",
        },
        []string{"schedule"},
    )

    RunsFinished = promauto.NewCounterVec(...)
    RunDuration  = promauto.NewHistogramVec(...)
    LLMTokens    = promauto.NewCounterVec(...)
    LLMCost      = promauto.NewCounterVec(...)
    DestPublish  = promauto.NewCounterVec(...)
)

// Handler returns the /metrics handler. Tests instantiate a fresh registry
// via promhttp.HandlerFor; production uses prometheus.DefaultGatherer.
func Handler() http.Handler { ... }
```

## Wiring

### Scheduler

`internal/scheduler/tick.go` increments `RunsStarted` after a successful
dispatch. The schedule name is already in scope.

### Runner / API finalize

`internal/api` finalize handler (the runner POSTs run completion to the
server) increments `RunsFinished`, `RunDuration.Observe`, `LLMTokens`,
`LLMCost` based on the submitted run summary.

### Publishers

Each adapter in `internal/publish/*` increments
`DestPublish{type=<name>, result=ok|error}` after attempting a publish.
The destination type is already a per-package constant.

### HTTP handler

`internal/webapi/server.go` `RegisterRoutes` adds:

```go
mux.Handle("GET /metrics", metrics.Handler())
```

Outside any middleware. The handler short-circuits before route matching
overhead is paid.

## Configuration

- `CRONFOUNDRY_METRICS_DISABLED` (bool, default `false`) — endpoint kill
  switch. When true, `metrics.Handler()` returns 404; producer-side
  increments still record into the registry (they're cheap and
  contention-free), so flipping the env var disables the scrape endpoint
  without stopping metric collection. Operators with strict no-metrics
  policies who need increments themselves stopped should not deploy this
  build, or omit the producer wiring at fork time.

That's it. No tunable scrape interval (Prometheus client side concern),
no histogram bucket overrides (we can revise in code if real data
requires).

## Tests

`internal/metrics/metrics_test.go`:

| Test | Asserts |
|---|---|
| `TestMetricsRegistered` | All 6 metric names appear in `prometheus.DefaultGatherer.Gather()` |
| `TestRunsStarted_Increments` | `RunsStarted.WithLabelValues("test").Inc()` shows up in scrape output |
| `TestHandler_PrometheusFormat` | `Handler().ServeHTTP` returns 200 with `Content-Type: text/plain; version=0.0.4` and `# HELP` / `# TYPE` lines |
| `TestHandler_Disabled` | With `Disabled = true`, returns 404 |
| `TestHistogramBuckets` | `RunDuration` exposes the configured 15-bucket histogram |

Wiring tests (one each in the relevant package):

- `internal/scheduler/tick_test.go` — after dispatch, `RunsStarted` count is +1.
- `internal/api/finalize_test.go` — after finalize, all four run-related
  metrics are +1 with the right labels.
- `internal/publish/<one>_test.go` — after a successful publish,
  `DestPublish{result=ok}` is +1.

## Operational

- No DB migration.
- No data migration. Metrics start at 0 on every restart — Prometheus
  servers handle counter resets correctly via `rate()`/`increase()`.
- Multi-replica concern: single-replica deploy today (scheduler
  limitation). Per-replica metrics on a future scale-out are scraped
  per-replica by Prometheus; aggregation is the scraper's job. No
  in-process coordination required.

## Documentation

`docs/guides/observability.md` (already exists) gets a new section
documenting:

- The endpoint URL (`/metrics`).
- The metric names + labels + meaning.
- Example PromQL queries: 7-day success rate, P95 duration, cost over
  time per provider.
- How to scrape from Azure Monitor managed Prometheus.

`README.md` operator-endpoints block adds:

```text
- GET /metrics — Prometheus text-format scrape endpoint
```

## Out of scope

- OpenTelemetry tracing.
- Custom buckets per histogram label.
- Push gateway support.
- Per-tenant metric labels (tenant model is single-tenant today).
- Alerting / dashboards.

## Acceptance criteria

1. `GET /metrics` returns 200 with valid Prometheus text format.
2. All 6 business metrics + Go runtime metrics are present in scrape output.
3. After running one schedule end-to-end (manual or e2e),
   `cronfoundry_runs_started_total` and `cronfoundry_runs_finished_total{status="succeeded"}`
   are both ≥ 1.
4. `CRONFOUNDRY_METRICS_DISABLED=true` makes `/metrics` return 404.
5. `go vet ./...` clean; new tests pass.
6. Documentation updated.
