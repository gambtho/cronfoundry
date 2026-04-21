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
