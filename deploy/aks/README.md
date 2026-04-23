# CronFoundry — AKS Deployment

## Prerequisites

- AKS cluster with OIDC issuer enabled and workload identity addon installed
- Azure Key Vault with a managed identity `cf-runner` that has `Key Vault Secrets User` role
- Federated credential on `cf-runner` pointing to the AKS OIDC issuer + `cf-runner` service account
- `kubectl` and `helm` installed and pointed at your cluster

## Steps

1. Create namespace:
   ```bash
   kubectl create namespace cronfoundry
   ```

2. Copy and fill in values:
   ```bash
   cp deploy/aks/chart/values.yaml my-values.yaml
   # Edit my-values.yaml — fill in all empty strings
   ```

3. Set `workloadIdentity.clientId` to the client ID of the `cf-runner` managed identity.

4. Set `K8S_RUNNER_IMAGE` to the same image tag you are deploying (e.g. `ghcr.io/cronfoundry/cronfoundry:v1.2.0`).

5. Deploy:
   ```bash
   helm install cronfoundry deploy/aks/chart \
     -n cronfoundry \
     -f my-values.yaml
   ```

6. Verify pods are running:
   ```bash
   kubectl get pods -n cronfoundry
   ```

## Upgrade

```bash
helm upgrade cronfoundry deploy/aks/chart -n cronfoundry -f my-values.yaml
```

## Secrets

Secrets are stored in Azure Key Vault. Set `AZURE_KEYVAULT_URL` to your vault's URL. The `cf-runner` service account must have `Key Vault Secrets User` role on the vault.
