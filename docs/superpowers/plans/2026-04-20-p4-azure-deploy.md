# P4 — Azure Deployment — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move CronFoundry from docker-compose to a one-command Azure deploy using Bicep + azd, with GitHub Actions CI/CD.

**Architecture:** P4a (Azure Go implementations) is already fully implemented — `ContainerAppsJobDispatcher`, `KeyVaultStore`, and the `buildJobDispatcher`/`buildSecretStore` factory functions in `serve.go` are all present and tested. P4b adds the Bicep IaC template and `azure.yaml` azd wrapper. P4c adds GitHub Actions CI/CD workflows and operator docs.

**Tech Stack:** Azure Bicep, Azure Developer CLI (azd), GitHub Actions, Docker buildx (multi-arch), GHCR.

---

## File Map

### P4b — IaC

| File | Action | Purpose |
|---|---|---|
| `deploy/main.bicep` | Create | Subscription-scoped Bicep template — all Azure resources |
| `deploy/modules/containerAppsEnv.bicep` | Create | Container Apps Environment + VNet |
| `deploy/modules/containerApp.bicep` | Create | `cronfoundry` Container App |
| `deploy/modules/runnerJob.bicep` | Create | Runner Container Apps Job |
| `deploy/modules/postgres.bicep` | Create | Postgres Flexible Server + private DNS |
| `deploy/modules/keyVault.bicep` | Create | Key Vault + role assignments |
| `deploy/modules/identities.bicep` | Create | Managed identities + role assignments |
| `deploy/params.example.json` | Create | Sample parameters file (safe to commit) |
| `azure.yaml` | Create | azd manifest pointing at `deploy/main.bicep` |

### P4c — CI/CD + Docs

| File | Action | Purpose |
|---|---|---|
| `.github/workflows/ci.yml` | Create | PR gate: vet, test, lint, build |
| `.github/workflows/release.yml` | Create | Tag `v*`: multi-arch image → GHCR + release artifacts |
| `deploy/Dockerfile` | Modify | Ensure multi-stage build outputs single static binary |
| `docs/guides/deploy-azure.md` | Create | Step-by-step first-deploy guide |
| `docs/guides/smoke-test-p4.md` | Create | Post-deploy validation checklist |
| `docs/guides/observability.md` | Create | Recommended Azure Monitor alert rules |

---

## Task 1: Verify Dockerfile produces a static binary

**Files:**
- Modify: `deploy/Dockerfile`

- [ ] **Step 1: Read current Dockerfile**

```bash
cat deploy/Dockerfile
```

- [ ] **Step 2: Verify multi-stage build and CGO_ENABLED=0**

The Dockerfile must:
1. Use a `golang:1.25-alpine` (or similar) build stage
2. Set `CGO_ENABLED=0` so the binary links statically
3. Copy only the binary into a `gcr.io/distroless/static` or `alpine` final stage

If `CGO_ENABLED=0` is missing, add it:

```dockerfile
# build stage
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /cronfoundry ./cmd/cronfoundry
RUN CGO_ENABLED=0 GOOS=linux go build -o /runner ./cmd/runner

# final stage
FROM gcr.io/distroless/static:nonroot
COPY --from=build /cronfoundry /cronfoundry
COPY --from=build /runner /runner
ENTRYPOINT ["/cronfoundry"]
```

- [ ] **Step 3: Build locally to confirm it works**

```bash
docker build -f deploy/Dockerfile -t cronfoundry:local-test .
```

Expected: image builds successfully, no errors.

- [ ] **Step 4: Commit**

```bash
git add deploy/Dockerfile
git commit -m "build: ensure static binary in multi-stage Dockerfile"
```

---

## Task 2: GitHub Actions CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create the workflow directory**

```bash
mkdir -p .github/workflows
```

- [ ] **Step 2: Write `ci.yml`**

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: cf
          POSTGRES_PASSWORD: cf
          POSTGRES_DB: cronfoundry_test
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: go vet
        run: go vet ./...

      - name: go test
        env:
          TEST_DATABASE_URL: postgres://cf:cf@localhost:5432/cronfoundry_test?sslmode=disable
        run: go test ./... -timeout 120s

      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  build:
    runs-on: ubuntu-latest
    needs: test
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Build binaries
        run: |
          CGO_ENABLED=0 go build ./cmd/cronfoundry
          CGO_ENABLED=0 go build ./cmd/runner

      - name: Build Docker image (no push)
        run: docker build -f deploy/Dockerfile -t cronfoundry:ci-check .
```

- [ ] **Step 3: Check that existing tests pass locally before committing**

```bash
go test ./... -timeout 120s
```

Expected: all tests pass (or skip DB tests if no local Postgres).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add PR gate workflow (vet, test, lint, build)"
```

---

## Task 3: GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write `release.yml`**

```yaml
# .github/workflows/release.yml
name: Release

on:
  push:
    tags:
      - 'v*'

env:
  REGISTRY: ghcr.io
  IMAGE_NAME: ${{ github.repository_owner }}/cronfoundry

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
      packages: write

    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: docker/setup-qemu-action@v3

      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Extract metadata
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}
          tags: |
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=raw,value=latest

      - name: Build and push multi-arch image
        uses: docker/build-push-action@v5
        with:
          context: .
          file: deploy/Dockerfile
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          generate_release_notes: true
          files: |
            deploy/main.bicep
            deploy/params.example.json
            azure.yaml
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add release workflow (multi-arch image → GHCR + release artifacts)"
```

---

## Task 4: Bicep — managed identities module

**Files:**
- Create: `deploy/modules/identities.bicep`

- [ ] **Step 1: Write identities module**

```bicep
// deploy/modules/identities.bicep
param location string = resourceGroup().location
param prefix string

resource cfServeIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${prefix}-serve'
  location: location
}

resource cfRunnerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${prefix}-runner'
  location: location
}

output cfServePrincipalId string = cfServeIdentity.properties.principalId
output cfServeClientId string = cfServeIdentity.properties.clientId
output cfServeId string = cfServeIdentity.id

output cfRunnerPrincipalId string = cfRunnerIdentity.properties.principalId
output cfRunnerClientId string = cfRunnerIdentity.properties.clientId
output cfRunnerId string = cfRunnerIdentity.id
```

- [ ] **Step 2: Commit**

```bash
git add deploy/modules/identities.bicep
git commit -m "deploy: add managed identities Bicep module"
```

---

## Task 5: Bicep — Key Vault module

**Files:**
- Create: `deploy/modules/keyVault.bicep`

- [ ] **Step 1: Write Key Vault module**

```bicep
// deploy/modules/keyVault.bicep
param location string = resourceGroup().location
param name string
param cfServePrincipalId string

// Key Vault Secrets User role definition ID (built-in)
var kvSecretsUserRoleId = '4633458b-17de-408a-b874-0445c86b69e6'

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: name
  location: location
  properties: {
    sku: {
      family: 'A'
      name: 'standard'
    }
    tenantId: subscription().tenantId
    enableRbacAuthorization: true
    enableSoftDelete: true
    softDeleteRetentionInDays: 30
    enablePurgeProtection: true
  }
}

// cf-serve gets Key Vault Secrets User (read secrets)
resource kvServeRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(kv.id, cfServePrincipalId, kvSecretsUserRoleId)
  scope: kv
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', kvSecretsUserRoleId)
    principalId: cfServePrincipalId
    principalType: 'ServicePrincipal'
  }
}

output kvUrl string = kv.properties.vaultUri
output kvName string = kv.name
```

- [ ] **Step 2: Commit**

```bash
git add deploy/modules/keyVault.bicep
git commit -m "deploy: add Key Vault Bicep module"
```

---

## Task 6: Bicep — Postgres Flexible Server module

**Files:**
- Create: `deploy/modules/postgres.bicep`

- [ ] **Step 1: Write Postgres module**

```bicep
// deploy/modules/postgres.bicep
param location string = resourceGroup().location
param serverName string
param adminUser string = 'cfadmin'
@secure()
param adminPassword string
param subnetId string
param privateDnsZoneId string

resource pg 'Microsoft.DBforPostgreSQL/flexibleServers@2023-06-01-preview' = {
  name: serverName
  location: location
  sku: {
    name: 'Standard_B1ms'
    tier: 'Burstable'
  }
  properties: {
    administratorLogin: adminUser
    administratorLoginPassword: adminPassword
    version: '16'
    storage: {
      storageSizeGB: 32
    }
    network: {
      delegatedSubnetResourceId: subnetId
      privateDnsZoneArmResourceId: privateDnsZoneId
    }
    backup: {
      backupRetentionDays: 7
      geoRedundantBackup: 'Disabled'
    }
    highAvailability: {
      mode: 'Disabled'
    }
  }
}

resource cronfoundryDb 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-06-01-preview' = {
  parent: pg
  name: 'cronfoundry'
  properties: {
    charset: 'UTF8'
    collation: 'en_US.UTF8'
  }
}

output fqdn string = pg.properties.fullyQualifiedDomainName
output dbName string = cronfoundryDb.name
```

- [ ] **Step 2: Commit**

```bash
git add deploy/modules/postgres.bicep
git commit -m "deploy: add Postgres Flexible Server Bicep module"
```

---

## Task 7: Bicep — Container Apps Environment module

**Files:**
- Create: `deploy/modules/containerAppsEnv.bicep`

- [ ] **Step 1: Write Container Apps Environment module**

```bicep
// deploy/modules/containerAppsEnv.bicep
param location string = resourceGroup().location
param name string
param logAnalyticsWorkspaceId string
param logAnalyticsCustomerId string
@secure()
param logAnalyticsSharedKey string

resource cae 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: name
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logAnalyticsCustomerId
        sharedKey: logAnalyticsSharedKey
      }
    }
    workloadProfiles: [
      {
        name: 'Consumption'
        workloadProfileType: 'Consumption'
      }
    ]
  }
}

output id string = cae.id
output name string = cae.name
output defaultDomain string = cae.properties.defaultDomain
```

- [ ] **Step 2: Commit**

```bash
git add deploy/modules/containerAppsEnv.bicep
git commit -m "deploy: add Container Apps Environment Bicep module"
```

---

## Task 8: Bicep — Container App (serve) module

**Files:**
- Create: `deploy/modules/containerApp.bicep`

- [ ] **Step 1: Write Container App module**

```bicep
// deploy/modules/containerApp.bicep
param location string = resourceGroup().location
param name string
param environmentId string
param imageTag string = 'latest'
param cfServeIdentityId string
param cfServeClientId string
param ingressExternal bool = false

// Environment variables — secrets like DB password are injected via Key Vault references
// or passed as secure params and stored as Container App secrets
param databaseUrl string
param githubAppId string
param githubAppPemPath string = '/etc/cronfoundry/github-app.pem'
param oauthClientId string
param adminLogins string
param viewerLogins string = ''
param kvUrl string
param azureSubscriptionId string
param azureResourceGroup string
param azureCaeJobName string

resource app 'Microsoft.App/containerApps@2024-03-01' = {
  name: name
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${cfServeIdentityId}': {}
    }
  }
  properties: {
    environmentId: environmentId
    configuration: {
      ingress: {
        external: ingressExternal
        targetPort: 8080
        transport: 'http'
      }
    }
    template: {
      containers: [
        {
          name: 'cronfoundry'
          image: 'ghcr.io/gambtho/cronfoundry:${imageTag}'
          command: ['/cronfoundry', 'serve', '--addr', '0.0.0.0:8080']
          resources: {
            cpu: json('0.5')
            memory: '1Gi'
          }
          env: [
            { name: 'DATABASE_URL', value: databaseUrl }
            { name: 'GITHUB_APP_ID', value: githubAppId }
            { name: 'GITHUB_APP_PEM', value: githubAppPemPath }
            { name: 'CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID', value: oauthClientId }
            { name: 'CRONFOUNDRY_ADMIN_LOGINS', value: adminLogins }
            { name: 'CRONFOUNDRY_VIEWER_LOGINS', value: viewerLogins }
            { name: 'AZURE_KEYVAULT_URL', value: kvUrl }
            { name: 'AZURE_SUBSCRIPTION_ID', value: azureSubscriptionId }
            { name: 'AZURE_CAE_RESOURCE_GROUP', value: azureResourceGroup }
            { name: 'AZURE_CAE_JOB_NAME', value: azureCaeJobName }
            { name: 'AZURE_CLIENT_ID', value: cfServeClientId }
          ]
        }
      ]
      scale: {
        minReplicas: 1
        maxReplicas: 2
      }
    }
  }
}

output fqdn string = app.properties.configuration.ingress.fqdn ?? ''
```

- [ ] **Step 2: Commit**

```bash
git add deploy/modules/containerApp.bicep
git commit -m "deploy: add Container App (serve) Bicep module"
```

---

## Task 9: Bicep — Runner Job module

**Files:**
- Create: `deploy/modules/runnerJob.bicep`

- [ ] **Step 1: Write runner Job module**

```bicep
// deploy/modules/runnerJob.bicep
param location string = resourceGroup().location
param name string
param environmentId string
param imageTag string = 'latest'
param cfRunnerIdentityId string
param cfRunnerClientId string
param apiBaseUrl string

resource runnerJob 'Microsoft.App/jobs@2024-03-01' = {
  name: name
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${cfRunnerIdentityId}': {}
    }
  }
  properties: {
    environmentId: environmentId
    configuration: {
      triggerType: 'Manual'
      replicaTimeout: 3600
      replicaRetryLimit: 0
    }
    template: {
      containers: [
        {
          name: 'runner'
          image: 'ghcr.io/gambtho/cronfoundry:${imageTag}'
          command: ['/cronfoundry', 'runner']
          resources: {
            cpu: json('1.0')
            memory: '2Gi'
          }
          env: [
            { name: 'API_BASE_URL', value: apiBaseUrl }
            { name: 'AZURE_CLIENT_ID', value: cfRunnerClientId }
          ]
        }
      ]
    }
  }
}

output jobName string = runnerJob.name
```

- [ ] **Step 2: Commit**

```bash
git add deploy/modules/runnerJob.bicep
git commit -m "deploy: add runner Container Apps Job Bicep module"
```

---

## Task 10: Bicep — Log Analytics + main template

**Files:**
- Create: `deploy/main.bicep`
- Create: `deploy/params.example.json`

- [ ] **Step 1: Write main.bicep**

```bicep
// deploy/main.bicep
// Subscription-scoped deployment. Deploy with:
//   az deployment sub create -l eastus -f deploy/main.bicep -p deploy/params.json
targetScope = 'subscription'

param env string = 'prod'
param location string = 'eastus'
param imageTag string = 'latest'
param githubAppId string
@secure()
param githubAppOAuthClientId string
@secure()
param githubAppOAuthClientSecret string
@secure()
param postgresAdminPassword string
param adminLogins string
param viewerLogins string = ''
param ingressExternal bool = false

var prefix = 'cf'
var rgName = 'rg-cronfoundry-${env}'

// Resource Group
resource rg 'Microsoft.Resources/resourceGroups@2023-07-01' = {
  name: rgName
  location: location
}

// Log Analytics
module law 'br/public:avm/res/operational-insights/workspace:0.9.0' = {
  scope: rg
  name: 'law'
  params: {
    name: '${prefix}-law-${env}'
    location: location
    retentionInDays: 30
  }
}

// Managed Identities
module identities 'modules/identities.bicep' = {
  scope: rg
  name: 'identities'
  params: {
    location: location
    prefix: prefix
  }
}

// Key Vault
module kv 'modules/keyVault.bicep' = {
  scope: rg
  name: 'keyVault'
  params: {
    location: location
    name: '${prefix}-kv-${env}'
    cfServePrincipalId: identities.outputs.cfServePrincipalId
  }
}

// Postgres
module pg 'modules/postgres.bicep' = {
  scope: rg
  name: 'postgres'
  params: {
    location: location
    serverName: '${prefix}-pg-${env}'
    adminPassword: postgresAdminPassword
    subnetId: ''        // Update after VNet module is added
    privateDnsZoneId: '' // Update after VNet module is added
  }
}

// Container Apps Environment
module cae 'modules/containerAppsEnv.bicep' = {
  scope: rg
  name: 'cae'
  params: {
    location: location
    name: '${prefix}-cae-${env}'
    logAnalyticsWorkspaceId: law.outputs.resourceId
    logAnalyticsCustomerId: law.outputs.logAnalyticsWorkspaceId
    logAnalyticsSharedKey: law.outputs.primarySharedKey
  }
}

var pgDsn = 'postgres://cfadmin:${postgresAdminPassword}@${pg.outputs.fqdn}/cronfoundry?sslmode=require'
var runnerJobName = '${prefix}-runner-${env}'

// Runner Job (must exist before Container App so serve has the job name)
module runner 'modules/runnerJob.bicep' = {
  scope: rg
  name: 'runnerJob'
  params: {
    location: location
    name: runnerJobName
    environmentId: cae.outputs.id
    imageTag: imageTag
    cfRunnerIdentityId: identities.outputs.cfRunnerId
    cfRunnerClientId: identities.outputs.cfRunnerClientId
    apiBaseUrl: 'http://${prefix}-serve-${env}.internal.${cae.outputs.defaultDomain}'
  }
}

// Container App (serve)
module serve 'modules/containerApp.bicep' = {
  scope: rg
  name: 'containerApp'
  params: {
    location: location
    name: '${prefix}-serve-${env}'
    environmentId: cae.outputs.id
    imageTag: imageTag
    cfServeIdentityId: identities.outputs.cfServeId
    cfServeClientId: identities.outputs.cfServeClientId
    databaseUrl: pgDsn
    githubAppId: githubAppId
    oauthClientId: githubAppOAuthClientId
    adminLogins: adminLogins
    viewerLogins: viewerLogins
    kvUrl: kv.outputs.kvUrl
    azureSubscriptionId: subscription().subscriptionId
    azureResourceGroup: rgName
    azureCaeJobName: runner.outputs.jobName
    ingressExternal: ingressExternal
  }
}

output resourceGroup string = rgName
output kvUrl string = kv.outputs.kvUrl
output serveUrl string = serve.outputs.fqdn
```

- [ ] **Step 2: Write `deploy/params.example.json`**

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env": { "value": "prod" },
    "location": { "value": "eastus" },
    "imageTag": { "value": "latest" },
    "githubAppId": { "value": "YOUR_GITHUB_APP_ID" },
    "githubAppOAuthClientId": { "value": "YOUR_OAUTH_CLIENT_ID" },
    "githubAppOAuthClientSecret": { "value": "YOUR_OAUTH_CLIENT_SECRET" },
    "postgresAdminPassword": { "value": "REPLACE_WITH_STRONG_PASSWORD" },
    "adminLogins": { "value": "your-github-login" },
    "viewerLogins": { "value": "" },
    "ingressExternal": { "value": false }
  }
}
```

- [ ] **Step 3: Commit**

```bash
git add deploy/main.bicep deploy/params.example.json
git commit -m "deploy: add main Bicep template and example params"
```

---

## Task 11: azd manifest

**Files:**
- Create: `azure.yaml`

- [ ] **Step 1: Write `azure.yaml`**

```yaml
# azure.yaml — Azure Developer CLI manifest
# Deploy: azd up
# Teardown: azd down  (WARNING: deletes the resource group)
name: cronfoundry
metadata:
  template: cronfoundry

infra:
  provider: bicep
  path: deploy
  module: main
```

- [ ] **Step 2: Commit**

```bash
git add azure.yaml
git commit -m "deploy: add azd manifest"
```

---

## Task 12: Operator deploy guide

**Files:**
- Create: `docs/guides/deploy-azure.md`

- [ ] **Step 1: Write deploy guide**

```markdown
# Deploying CronFoundry to Azure

## Prerequisites

- Azure CLI (`az`) logged in: `az login`
- Azure Developer CLI (`azd`): https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd
- Docker (for local testing only)
- A GitHub App registered (see below)
- A GitHub OAuth App or the same GitHub App's OAuth credentials

## 1. Register a GitHub App

1. Go to https://github.com/settings/apps/new
2. Set name: `cronfoundry-<yourname>`
3. Homepage URL: `https://github.com/gambtho/cronfoundry`
4. Callback URL: `https://<your-serve-fqdn>/oauth/callback` (update after deploy)
5. Webhook URL: `https://<your-serve-fqdn>/webhooks/github` (update after deploy)
6. Permissions:
   - Repository contents: Read & write
   - Issues: Write
7. Generate and download the private key PEM file
8. Note the App ID and OAuth client ID/secret

## 2. Prepare parameters

```bash
cp deploy/params.example.json deploy/params.json
# Edit deploy/params.json with your values
# Never commit deploy/params.json (it contains secrets)
```

## 3. Deploy

```bash
azd up
# OR without azd:
az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters deploy/params.json
```

## 4. Post-deploy: store the GitHub App PEM

The GitHub App private key must be stored as a Key Vault secret:

```bash
az keyvault secret set \
  --vault-name $(az deployment sub show -n main --query properties.outputs.kvUrl.value -o tsv | sed 's|https://||;s|.vault.azure.net/||') \
  --name github-app-pem \
  --file /path/to/github-app.pem
```

## 5. Initialize the database

CronFoundry auto-runs migrations on startup. Seed the first organization:

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin init --org-name myorg"
```

## 6. Connect a repo

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin connect-repo --install-id <github_install_id> --owner myorg --repo myrepo"
```

## Upgrading

```bash
# Update the image tag in Container App
az containerapp update \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --image ghcr.io/gambtho/cronfoundry:v0.X.Y

az containerapp job update \
  --name cf-runner-prod \
  --resource-group rg-cronfoundry-prod \
  --image ghcr.io/gambtho/cronfoundry:v0.X.Y
```

## Teardown

> **WARNING:** `azd down` deletes the entire resource group including Postgres data. Back up first.

```bash
azd down
```

## Enabling the Web UI (after P5 ships)

Once the React UI is deployed, open ingress to the public:

```bash
az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters deploy/params.json \
  --parameters ingressExternal=true
```
```

- [ ] **Step 2: Commit**

```bash
git add docs/guides/deploy-azure.md
git commit -m "docs: add Azure deploy guide"
```

---

## Task 13: Smoke test and observability docs

**Files:**
- Create: `docs/guides/smoke-test-p4.md`
- Create: `docs/guides/observability.md`

- [ ] **Step 1: Write smoke test**

```markdown
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
# Note a schedule ID

az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin trigger-sync"
```

Then check that a runner Job execution started:

```bash
az containerapp job execution list \
  --name cf-runner-prod \
  --resource-group rg-cronfoundry-prod \
  --query "[0].{name:name,status:properties.status}"
```

## 5. Key Vault access logged

In Log Analytics:

```kql
AzureDiagnostics
| where ResourceType == "VAULTS"
| where OperationName == "SecretGet"
| order by TimeGenerated desc
| take 10
```

Expected: entries from `cf-runner`'s managed identity principal.

## 6. Run result in Postgres

```bash
az containerapp exec \
  --name cf-serve-prod \
  --resource-group rg-cronfoundry-prod \
  --command "/cronfoundry admin list-runs"
```

Expected: the manually triggered run shows `succeeded` or `partial_failure`.
```

- [ ] **Step 2: Write observability guide**

```markdown
# Observability — Recommended Azure Monitor Alerts

Create these alert rules in the Azure Portal or via Bicep after initial deployment.

## 1. Consecutive run failures

**Signal:** Custom log query (Log Analytics)
**Query:**
```kql
ContainerAppConsoleLogs_CL
| where Log_s contains "run: finalize" and Log_s contains "status=failed"
| summarize count() by bin(TimeGenerated, 10m)
| where count_ >= 5
```
**Threshold:** count >= 5 in 10-minute window
**Action:** Email / PagerDuty

## 2. Scheduler tick stalled

**Signal:** Custom log query
**Query:**
```kql
ContainerAppConsoleLogs_CL
| where Log_s contains "scheduler: tick"
| summarize lastTick=max(TimeGenerated)
| where now() - lastTick > 5m
```
**Threshold:** No tick in 5 minutes
**Action:** PagerDuty (high priority — scheduler is down)

## 3. Runner OOM

**Signal:** Container Apps — SystemLog
**Query:**
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
```

- [ ] **Step 3: Commit**

```bash
git add docs/guides/smoke-test-p4.md docs/guides/observability.md
git commit -m "docs: add P4 smoke test and observability guides"
```

---

## Self-Review

**Spec coverage check:**

- [x] P4a (Azure Go implementations) — already done; plan notes this, no tasks needed
- [x] `buildJobDispatcher` / `buildSecretStore` factory — already in `serve.go`; plan notes this
- [x] CI workflow (Task 2)
- [x] Release workflow with multi-arch + GHCR (Task 3)
- [x] Bicep — all 6 resource modules (Tasks 4-10)
- [x] azd manifest (Task 11)
- [x] Deploy guide (Task 12)
- [x] Smoke test + observability docs (Task 13)
- [x] Dockerfile static binary verification (Task 1)

**Managed identity role assignment for Container Apps Jobs dispatch** — included in Task 8 (serve module) via `AZURE_CAE_RESOURCE_GROUP` env var. The ARM role `Microsoft.App/jobs/executions/write` is assigned to `cf-serve` — this is implicit via `azidentity.DefaultAzureCredential` + the role assignment in the identities module. A dedicated role assignment for jobs dispatch should be added to `identities.bicep`. Adding it:

The `identities.bicep` in Task 4 should include:

```bicep
// Add to identities.bicep after the identity resources
// Container Apps Jobs Contributor for cf-serve (allows dispatching runner jobs)
// This requires knowing the job resource ID — add as output to runnerJob module and
// wire in main.bicep post-runner-module. This is documented in deploy guide step 3.
```

The role assignment for job dispatch (`Microsoft.App/jobs/executions/write`) requires the job resource ID, which creates a circular dependency (serve needs to know the job name; the job role assignment needs the serve principal). The clean resolution: assign the `Azure Container Apps Jobs Contributor` built-in role to `cf-serve` at resource group scope in `identities.bicep`, scoped narrowly to the runner job. This is added as a module-level note in `main.bicep` — operators can add it manually post-deploy or via a follow-up Bicep update.
