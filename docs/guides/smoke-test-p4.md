# Smoke Test — P4 Azure Deploy

After deploying with `azd up`, run through this checklist.

## 1. Container App is healthy

```bash
az containerapp show \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --query properties.runningStatus
```

Expected: `Running`

## 2. Health endpoint responds

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "wget -qO- http://localhost:8080/healthz"
```

Expected: `ok`

## 3. Scheduler is ticking (Log Analytics)

In the Azure Portal → Log Analytics Workspace → Logs:

```kql
ContainerAppConsoleLogs_CL
| where ContainerName_s == "cronfoundry"
| where Log_s contains "scheduler"
| order by TimeGenerated desc
| take 20
```

Expected: entries showing `scheduler: tick` every ~30 seconds.

## 4. Trigger a manual run

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin list-schedules"
# Note a repo reference (owner/name) from the listed schedules

az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin trigger-sync <owner/name>"
```

Then confirm a runner Job execution started:

```bash
az containerapp job execution list \
  --name cf-runner-prod \
  --resource-group rg-cronfoundry-prod \
  --query "[0].{name:name,status:properties.status}"
```

## 5. Key Vault access logged

> **Prerequisite:** Key Vault diagnostic settings must be enabled to route audit logs to the Log
> Analytics workspace. Enable via the Azure Portal (Key Vault → Diagnostic settings → Add setting →
> select `AuditEvent` → send to your Log Analytics workspace) or add a
> `Microsoft.Insights/diagnosticSettings` resource to `deploy/modules/keyVault.bicep`.

In Log Analytics:

```kql
AzureDiagnostics
| where ResourceType == "VAULTS"
| where OperationName == "SecretGet"
| order by TimeGenerated desc
| take 10
```

Expected: entries from `cf-serve`'s managed identity principal.

## 6. Run result in Postgres

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin list-runs"
```

Expected: the manually triggered run shows `succeeded` or `partial_failure`.
