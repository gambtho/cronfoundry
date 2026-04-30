# Observability — Recommended Azure Monitor Alerts

Create these alert rules in the Azure Portal or via Bicep after initial deployment.

## 1. Consecutive run failures

**Signal:** Custom log query (Log Analytics)

```kql
ContainerAppConsoleLogs_CL
| where Log_s contains "run: finalize" and Log_s contains "status=failed"
| summarize count() by bin(TimeGenerated, 10m)
| where count_ >= 5
```

**Threshold:** count >= 5 in a 10-minute window
**Action:** Email / PagerDuty

## 2. Scheduler tick stalled

**Signal:** Custom log query

```kql
ContainerAppConsoleLogs_CL
| where TimeGenerated > ago(15m)
| where Log_s contains "scheduler: tick"
| summarize lastTick = max(TimeGenerated)
| extend lastTick = coalesce(lastTick, datetime(1970-01-01))
| where now() - lastTick > 5m
```

**Threshold:** Number of results >= 1 (using a fallback ensures a row is returned even when the scheduler is fully silent)
**Evaluation frequency:** <= 5 minutes
**Action:** PagerDuty (high priority — scheduler is down)

## 3. Runner OOM

**Signal:** Container Apps system log

```kql
ContainerAppSystemLogs_CL
| where Reason_s == "OOMKilling" and ContainerAppName_s contains "runner"
```

**Threshold:** Any occurrence
**Action:** Email

## 4. Cost-per-run anomaly

**Signal:** Custom metric (emit from runner via OpenTelemetry `run.cost` metric)
**Threshold:** p95 cost_cents > 3× 7-day median
**Action:** Email (informational)

---

## Prometheus `/metrics` endpoint

The serve container exposes a Prometheus scrape endpoint at `/metrics`.
Unauthenticated; emits the standard Prometheus text format.

### Configuring scrape

Azure Monitor managed Prometheus scrapes any HTTP endpoint serving the
Prometheus exposition format; point a `PodMonitor` or `ScrapeConfig` at
`https://<your-fqdn>/metrics`. Scrape interval 30-60s is plenty.

For self-managed Prometheus:

```yaml
scrape_configs:
  - job_name: cronfoundry
    metrics_path: /metrics
    static_configs:
      - targets: ["cronfoundry.example.com"]
```

### Metrics emitted

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `cronfoundry_runs_started_total` | counter | `schedule` | Run dispatches by the scheduler |
| `cronfoundry_runs_finished_total` | counter | `status` | Terminal run states (`succeeded`, `partial_failure`, `failed`) |
| `cronfoundry_run_duration_seconds` | histogram | `status` | Wall-clock dispatch→finalize time |
| `cronfoundry_llm_tokens_total` | counter | `provider`, `kind` | LLM tokens (`input` or `output`); provider is currently `unknown` until plumbed through finalize |
| `cronfoundry_llm_cost_usd_total` | counter | `provider` | Estimated USD cost |
| `cronfoundry_destination_publish_total` | counter | `type`, `result` | Publish attempts (`ok` or `error`); skipped destinations not counted |

Plus standard Go runtime metrics (`go_goroutines`, `go_gc_duration_seconds`,
`process_resident_memory_bytes`, etc.) that ship for free with promhttp.

### Example queries

7-day run success rate:
```promql
sum(rate(cronfoundry_runs_finished_total{status="succeeded"}[7d]))
  / sum(rate(cronfoundry_runs_finished_total[7d]))
```

P95 run duration:
```promql
histogram_quantile(0.95, sum by (le) (rate(cronfoundry_run_duration_seconds_bucket[1h])))
```

Daily LLM cost by provider:
```promql
sum by (provider) (increase(cronfoundry_llm_cost_usd_total[1d]))
```

Destination failure rate (last hour):
```promql
sum by (type) (rate(cronfoundry_destination_publish_total{result="error"}[1h]))
  / sum by (type) (rate(cronfoundry_destination_publish_total[1h]))
```

### Disabling

Set `CRONFOUNDRY_METRICS_DISABLED=true` to make `/metrics` return 404. The
internal counters still increment (cheap), so the toggle is reversible
without a restart-then-restart hop.
