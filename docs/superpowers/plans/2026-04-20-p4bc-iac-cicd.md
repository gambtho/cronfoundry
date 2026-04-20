# P4b/c — Bicep IaC + CI/CD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a single-command Azure deployment (Bicep template), multi-arch Docker images on GHCR, and two GitHub Actions workflows (PR gate + release with IaC artifacts attached).

**Architecture:** One `deploy/main.bicep` (subscription-level, creates `rg-cronfoundry-{env}`). Two managed identities (`cf-serve`, `cf-runner`). Multi-arch image from existing Dockerfile. `ci.yml` gates PRs; `release.yml` publishes on `v*` tags.

**Tech Stack:** Bicep, Azure CLI, GitHub Actions, Docker Buildx, GHCR.

---

## File Map

**New files:**
- `deploy/main.bicep` — subscription-level Bicep template
- `deploy/params.example.json` — parameter template
- `deploy/README.md` — operator setup guide
- `.github/workflows/ci.yml` — PR gate
- `.github/workflows/release.yml` — tag → GHCR + GitHub release
- `docs/guides/smoke-test-p4.md` — Azure smoke test runbook

**Modified files:**
- `deploy/Dockerfile` — add multi-stage frontend build (P3b adds web/dist; Dockerfile needs to build web first)
- `Makefile` — add `web-build` target dependency

---

## Task 1: Update Dockerfile for multi-stage web + Go build

**Files:**
- Modify: `deploy/Dockerfile`
- Modify: `Makefile`

- [ ] **Step 1: Update Dockerfile**

Replace `deploy/Dockerfile`:

```dockerfile
# Stage 1: build React frontend
FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: build Go binary
FROM golang:1.25-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/cronfoundry ./cmd/cronfoundry

# Stage 3: runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/cronfoundry /cronfoundry
USER nonroot
ENTRYPOINT ["/cronfoundry"]
```

- [ ] **Step 2: Build and verify image**

```bash
cd /home/tng/workspace/cronfoundry
docker build -f deploy/Dockerfile -t cronfoundry:local .
docker run --rm cronfoundry:local --help
```

Expected: prints cronfoundry help text.

- [ ] **Step 3: Update Makefile**

Add `web-build` target:

```makefile
web-build:
	cd web && npm ci && npm run build

build: web-build
	go build -o cronfoundry-runner ./cmd/runner
	go build -o cronfoundry       ./cmd/cronfoundry
```

- [ ] **Step 4: Commit**

```bash
git add deploy/Dockerfile Makefile
git commit -m "build: multi-stage Dockerfile builds web frontend + Go binary"
```

---

## Task 2: GitHub Actions CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write ci.yml**

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: cronfoundry
          POSTGRES_PASSWORD: cronfoundry
          POSTGRES_DB: cronfoundry_test
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    env:
      CRONFOUNDRY_DATABASE_URL: postgres://cronfoundry:cronfoundry@localhost:5432/cronfoundry_test

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: go vet
        run: go vet ./...

      - name: go test
        run: go test ./... -count=1 -timeout 10m

      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest

      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: web/package-lock.json

      - name: Web install
        run: cd web && npm ci

      - name: Web build
        run: cd web && npm run build

      - name: Docker build check (no push)
        uses: docker/build-push-action@v5
        with:
          context: .
          file: deploy/Dockerfile
          platforms: linux/amd64,linux/arm64
          push: false
```

- [ ] **Step 2: Verify syntax**

```bash
# Requires GitHub CLI
gh workflow view --yaml .github/workflows/ci.yml 2>/dev/null || echo "gh view not available, syntax check via yamllint"
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "YAML valid"
```

Expected: `YAML valid`

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: PR gate workflow (test, lint, web build, docker build check)"
```

---

## Task 3: GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write release.yml**

Create `.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write
  packages: write

jobs:
  release:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: cronfoundry
          POSTGRES_PASSWORD: cronfoundry
          POSTGRES_DB: cronfoundry_test
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    env:
      CRONFOUNDRY_DATABASE_URL: postgres://cronfoundry:cronfoundry@localhost:5432/cronfoundry_test

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: go vet
        run: go vet ./...

      - name: go test
        run: go test ./... -count=1 -timeout 10m

      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: web/package-lock.json

      - name: Web build
        run: cd web && npm ci && npm run build

      - name: Set up QEMU (multi-arch)
        uses: docker/setup-qemu-action@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push multi-arch image
        uses: docker/build-push-action@v5
        with:
          context: .
          file: deploy/Dockerfile
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/cronfoundry:${{ github.ref_name }}
            ghcr.io/${{ github.repository_owner }}/cronfoundry:latest

      - name: Create GitHub release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh release create ${{ github.ref_name }} \
            --generate-notes \
            --title "${{ github.ref_name }}" \
            deploy/main.bicep \
            deploy/params.example.json
```

- [ ] **Step 2: Verify YAML syntax**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))" && echo "YAML valid"
```

Expected: `YAML valid`

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: release workflow — multi-arch GHCR push + GitHub release with IaC artifacts"
```

---

## Task 4: Bicep template

**Files:**
- Create: `deploy/main.bicep`
- Create: `deploy/params.example.json`

- [ ] **Step 1: Write params.example.json**

Create `deploy/params.example.json`:

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env": { "value": "prod" },
    "location": { "value": "eastus" },
    "imageTag": { "value": "v0.1.0" },
    "postgresAdminPassword": { "value": "<CHANGE ME>" },
    "githubAppId": { "value": "<your GitHub App ID>" },
    "githubAppPrivateKeyBase64": { "value": "<base64-encoded PEM>" },
    "githubOAuthClientId": { "value": "<your GitHub OAuth client ID>" },
    "githubOAuthClientSecret": { "value": "<your GitHub OAuth client secret>" },
    "sessionMasterKey": { "value": "<32-byte hex string, e.g. from: openssl rand -hex 32>" },
    "adminLogins": { "value": ["your-github-username"] },
    "githubAppSlug": { "value": "<your GitHub App slug>" }
  }
}
```

- [ ] **Step 2: Write main.bicep**

Create `deploy/main.bicep`:

```bicep
targetScope = 'subscription'

@description('Environment name suffix (e.g. prod, staging)')
param env string = 'prod'

@description('Azure region')
param location string = 'eastus'

@description('Container image tag to deploy')
param imageTag string

@secure()
param postgresAdminPassword string

@description('GitHub App ID (numeric)')
param githubAppId string

@secure()
@description('GitHub App private key, base64-encoded PEM')
param githubAppPrivateKeyBase64 string

@description('GitHub OAuth App client ID')
param githubOAuthClientId string

@secure()
param githubOAuthClientSecret string

@secure()
@description('32-byte hex master key for session signing and envelope encryption')
param sessionMasterKey string

@description('GitHub logins allowed to log in as admin')
param adminLogins array = []

@description('GitHub App slug (used for installation URL)')
param githubAppSlug string

var rgName = 'rg-cronfoundry-${env}'
var prefix = 'cf${env}'

// ─── Resource Group ──────────────────────────────────────────────────────────

resource rg 'Microsoft.Resources/resourceGroups@2023-07-01' = {
  name: rgName
  location: location
}

// ─── Log Analytics ────────────────────────────────────────────────────────────

module logAnalytics 'br/public:avm/res/operational-insights/workspace:0.3.4' = {
  scope: rg
  name: '${prefix}-law'
  params: {
    name: '${prefix}-law'
    location: location
    dataRetention: 30
  }
}

// ─── Managed Identities ──────────────────────────────────────────────────────

resource idServe 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${prefix}-serve'
  location: location
  scope: rg
}

resource idRunner 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${prefix}-runner'
  location: location
  scope: rg
}

// ─── Key Vault ────────────────────────────────────────────────────────────────

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: '${prefix}-kv'
  location: location
  scope: rg
  properties: {
    sku: { family: 'A', name: 'standard' }
    tenantId: subscription().tenantId
    enableSoftDelete: true
    softDeleteRetentionInDays: 90
    enablePurgeProtection: true
    enableRbacAuthorization: true
  }
}

// Secrets stored in Key Vault
resource kvSecretPostgres 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: kv
  name: 'postgres-admin-password'
  properties: { value: postgresAdminPassword }
}

resource kvSecretGitHubPEM 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: kv
  name: 'github-app-pem-base64'
  properties: { value: githubAppPrivateKeyBase64 }
}

resource kvSecretOAuthSecret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: kv
  name: 'github-oauth-client-secret'
  properties: { value: githubOAuthClientSecret }
}

resource kvSecretMasterKey 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: kv
  name: 'session-master-key'
  properties: { value: sessionMasterKey }
}

// Grant cf-serve Key Vault Secrets Officer
resource roleKVServe 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(kv.id, idServe.id, 'b86a8fe4-44ce-4948-aee5-eccb2c155cd7')
  scope: kv
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b86a8fe4-44ce-4948-aee5-eccb2c155cd7') // Key Vault Secrets Officer
    principalId: idServe.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// ─── Postgres Flexible Server ─────────────────────────────────────────────────

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-06-01-preview' = {
  name: '${prefix}-pg'
  location: location
  scope: rg
  sku: { name: 'Standard_B1ms', tier: 'Burstable' }
  properties: {
    administratorLogin: 'cfadmin'
    administratorLoginPassword: postgresAdminPassword
    version: '16'
    storage: { storageSizeGB: 32 }
    backup: { backupRetentionDays: 7, geoRedundantBackup: 'Disabled' }
    network: {
      publicNetworkAccess: 'Disabled'
    }
  }
}

resource postgresDB 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-06-01-preview' = {
  parent: postgres
  name: 'cronfoundry'
  properties: { charset: 'UTF8', collation: 'en_US.utf8' }
}

// ─── Container Apps Environment ───────────────────────────────────────────────

resource cae 'Microsoft.App/managedEnvironments@2023-11-02-preview' = {
  name: '${prefix}-cae'
  location: location
  scope: rg
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logAnalytics.outputs.logAnalyticsWorkspaceId
        sharedKey: logAnalytics.outputs.logAnalyticsWorkspaceKey
      }
    }
  }
}

// ─── Container Apps Job (runner) ─────────────────────────────────────────────

resource runnerJob 'Microsoft.App/jobs@2023-11-02-preview' = {
  name: '${prefix}-runner'
  location: location
  scope: rg
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: { '${idRunner.id}': {} }
  }
  properties: {
    environmentId: cae.id
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
          resources: { cpu: json('1'), memory: '2Gi' }
          command: ['/cronfoundry', 'runner']
          env: [
            { name: 'CF_API_BASE_URL', value: 'https://${prefix}-api.${cae.properties.defaultDomain}' }
          ]
        }
      ]
    }
  }
}

// Grant cf-serve permission to start runner jobs
resource roleJobsServe 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(runnerJob.id, idServe.id, 'jobs-executor')
  scope: runnerJob
  properties: {
    // ContainerApps Job Executor — custom role or Contributor scoped to job
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b24988ac-6180-42a0-ab88-20f7382dd24c') // Contributor (scoped to job resource)
    principalId: idServe.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// ─── Container App (serve) ────────────────────────────────────────────────────

var postgresURL = 'postgres://cfadmin:${postgresAdminPassword}@${postgres.properties.fullyQualifiedDomainName}/cronfoundry?sslmode=require'

resource serveApp 'Microsoft.App/containerApps@2023-11-02-preview' = {
  name: '${prefix}-api'
  location: location
  scope: rg
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: { '${idServe.id}': {} }
  }
  properties: {
    environmentId: cae.id
    configuration: {
      ingress: {
        external: true
        targetPort: 8080
        transport: 'auto'
        allowInsecure: false
      }
      secrets: [
        {
          name: 'postgres-url'
          value: postgresURL
        }
        {
          name: 'github-app-pem'
          keyVaultUrl: kvSecretGitHubPEM.properties.secretUri
          identity: idServe.id
        }
        {
          name: 'oauth-client-secret'
          keyVaultUrl: kvSecretOAuthSecret.properties.secretUri
          identity: idServe.id
        }
        {
          name: 'master-key'
          keyVaultUrl: kvSecretMasterKey.properties.secretUri
          identity: idServe.id
        }
      ]
    }
    template: {
      scale: { minReplicas: 1, maxReplicas: 2 }
      containers: [
        {
          name: 'serve'
          image: 'ghcr.io/gambtho/cronfoundry:${imageTag}'
          resources: { cpu: json('0.5'), memory: '1Gi' }
          command: ['/cronfoundry', 'serve', '--addr', '0.0.0.0:8080']
          env: [
            { name: 'CRONFOUNDRY_DATABASE_URL', secretRef: 'postgres-url' }
            { name: 'CRONFOUNDRY_GITHUB_APP_ID', value: githubAppId }
            { name: 'CRONFOUNDRY_GITHUB_APP_PEM_BASE64', secretRef: 'github-app-pem' }
            { name: 'CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID', value: githubOAuthClientId }
            { name: 'CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET', secretRef: 'oauth-client-secret' }
            { name: 'CRONFOUNDRY_MASTER_KEY', secretRef: 'master-key' }
            { name: 'CRONFOUNDRY_ADMIN_LOGINS', value: join(adminLogins, ',') }
            { name: 'CRONFOUNDRY_GITHUB_APP_SLUG', value: githubAppSlug }
            { name: 'AZURE_KEYVAULT_URL', value: kv.properties.vaultUri }
            { name: 'AZURE_CAE_RESOURCE_GROUP', value: rgName }
            { name: 'AZURE_CAE_JOB_NAME', value: runnerJob.name }
            { name: 'AZURE_SUBSCRIPTION_ID', value: subscription().subscriptionId }
          ]
        }
      ]
    }
  }
}

// ─── Outputs ──────────────────────────────────────────────────────────────────

output appFQDN string = serveApp.properties.configuration.ingress.fqdn
output keyVaultName string = kv.name
output postgresServerName string = postgres.name
output resourceGroupName string = rgName
```

- [ ] **Step 3: Validate Bicep syntax**

```bash
az bicep build -f deploy/main.bicep --stdout > /dev/null && echo "Bicep valid"
```

Expected: `Bicep valid`. If `az bicep` not installed: `az bicep install` first.

- [ ] **Step 4: Commit**

```bash
git add deploy/main.bicep deploy/params.example.json
git commit -m "feat(deploy): Bicep template for Azure Container Apps deployment"
```

---

## Task 5: Bicep — handle PEM env var loading in serve.go

The Bicep template passes the GitHub App PEM as a base64-encoded env var (`CRONFOUNDRY_GITHUB_APP_PEM_BASE64`) since Key Vault secrets can't be mounted as files in Container Apps. The existing `serve.go` reads it from a file path. We need to support both.

**Files:**
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/cronfoundry/serve_test.go` (create if missing):

```go
package main

import (
	"encoding/base64"
	"testing"
)

func TestLoadPEM_FromBase64Env(t *testing.T) {
	pem := []byte("-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n")
	encoded := base64.StdEncoding.EncodeToString(pem)

	got, err := loadPEM("", encoded)
	if err != nil {
		t.Fatalf("loadPEM: %v", err)
	}
	if string(got) != string(pem) {
		t.Errorf("unexpected PEM bytes")
	}
}

func TestLoadPEM_FromFilePath(t *testing.T) {
	// Create a temp file
	f, err := os.CreateTemp("", "pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	pem := []byte("-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n")
	f.Write(pem)
	f.Close()

	got, err := loadPEM(f.Name(), "")
	if err != nil {
		t.Fatalf("loadPEM: %v", err)
	}
	if string(got) != string(pem) {
		t.Errorf("unexpected PEM bytes")
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
cd /home/tng/workspace/cronfoundry && go test ./cmd/cronfoundry/... -run TestLoadPEM -v
```

Expected: FAIL — `loadPEM` not defined.

- [ ] **Step 3: Implement loadPEM in serve.go**

Add to `cmd/cronfoundry/serve.go`:

```go
const envGitHubAppPEMBase64 = "CRONFOUNDRY_GITHUB_APP_PEM_BASE64"

// loadPEM reads the GitHub App private key either from a file (pemPath) or
// from a base64-encoded environment variable (pemBase64). pemPath takes
// precedence. Returns an error if neither is set.
func loadPEM(pemPath, pemBase64 string) ([]byte, error) {
	if pemPath != "" {
		return os.ReadFile(pemPath)
	}
	if pemBase64 != "" {
		return base64.StdEncoding.DecodeString(pemBase64)
	}
	return nil, fmt.Errorf("one of %s or %s must be set", envGitHubAppPEM, envGitHubAppPEMBase64)
}
```

Update the PEM loading in `runServe`:

```go
// Replace:
pemBytes, err := os.ReadFile(pemPath)
if err != nil {
    return fmt.Errorf("read PEM: %w", err)
}

// With:
pemBytes, err := loadPEM(pemPath, os.Getenv(envGitHubAppPEMBase64))
if err != nil {
    return fmt.Errorf("load PEM: %w", err)
}
```

Also relax the validation — `pemPath` is now optional if `pemBase64` is set:

```go
appID := os.Getenv(envGitHubAppID)
pemPath := os.Getenv(envGitHubAppPEM)
pemBase64 := os.Getenv(envGitHubAppPEMBase64)
if appID == "" {
    return fmt.Errorf("%s is required", envGitHubAppID)
}
if pemPath == "" && pemBase64 == "" {
    return fmt.Errorf("one of %s or %s is required", envGitHubAppPEM, envGitHubAppPEMBase64)
}
```

- [ ] **Step 4: Run tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./cmd/cronfoundry/... -run TestLoadPEM -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/cronfoundry/serve.go cmd/cronfoundry/serve_test.go
git commit -m "feat(serve): support CRONFOUNDRY_GITHUB_APP_PEM_BASE64 env var for Azure deploy"
```

---

## Task 6: Operator setup guide and smoke test runbook

**Files:**
- Create: `deploy/README.md`
- Create: `docs/guides/smoke-test-p4.md`

- [ ] **Step 1: Write deploy/README.md**

Create `deploy/README.md`:

```markdown
# CronFoundry — Azure Deployment

## Prerequisites

- Azure CLI (`az`) — [install](https://docs.microsoft.com/cli/azure/install-azure-cli)
- Active Azure subscription with `Owner` or `Contributor` role
- `az login` completed
- `az bicep install` (or `az bicep upgrade`)

## GitHub App Setup

Before deploying, you need a GitHub App. Create one at https://github.com/settings/apps/new:

**Permissions required:**
- Contents: Read & Write
- Issues: Write
- Metadata: Read

**Events to subscribe:**
- Push
- Installation

**Callback URL:** `https://<your-app-fqdn>/oauth/callback`
(You can set this after deploy — update it once you have the FQDN.)

After creating the App:
1. Note the **App ID** (numeric, on the app settings page)
2. Generate a **private key** (`.pem` file)
3. Convert to base64: `base64 -w0 your-app.private-key.pem`
4. Note the **Client ID** and generate a **Client secret** (OAuth tab)
5. Note the **App slug** (the URL-safe name, e.g. `cronfoundry-myorg`)

## Deploy

1. Copy `params.example.json` to `params.json` and fill in all values:

```bash
cp deploy/params.example.json deploy/params.json
# Edit params.json with your values
```

   Generate a master key:
   ```bash
   openssl rand -hex 32
   ```

2. Deploy:

```bash
az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters @deploy/params.json
```

3. Note the `appFQDN` output value — this is your CronFoundry URL.

4. Update your GitHub App's callback URL to `https://<appFQDN>/oauth/callback`.

5. Open `https://<appFQDN>` in your browser, log in with GitHub, and connect your first repo.

## Upgrade

To upgrade to a new version:

1. Update `imageTag` in `params.json`
2. Re-run the deployment command above — Bicep is idempotent

Migrations run automatically on Container App startup.

## Destroy

```bash
az group delete --name rg-cronfoundry-prod --yes
```

**Warning:** This deletes all resources including the Postgres database. Back up your data first.

## Local Development

See `deploy/docker-compose.yml` for local development without Azure.
```

- [ ] **Step 2: Write smoke test runbook**

Create `docs/guides/smoke-test-p4.md`:

```markdown
# P4 Azure Smoke Test Runbook

Run this after a fresh Azure deployment to verify end-to-end operation.

## Prerequisites

- Deployment complete (`az deployment sub create` succeeded)
- `appFQDN` output from the deployment (e.g. `cfprod-api.thankfulgrass-abc123.eastus.azurecontainerapps.io`)
- A GitHub account in the admin allowlist

## Steps

### 1. Verify the app is reachable

```bash
curl -s -o /dev/null -w "%{http_code}" https://<appFQDN>/healthz
# Expected: 200
```

### 2. Log in via GitHub OAuth

Open `https://<appFQDN>` in a browser. You should be redirected to GitHub for login.
After authorizing, you should land on the Overview page.

### 3. Connect a repo

1. Navigate to Repos → Connect repo
2. Install the GitHub App on a repo containing a `cronfoundry.yaml`
3. Verify the repo appears in the Repos list

### 4. Add an LLM API key secret

1. Navigate to Secrets → Add secret
2. Name: `OPENAI_API_KEY` (or your provider's key name)
3. Value: your API key
4. Verify it appears in the list (value not shown)

### 5. Verify schedules appear

Navigate to Schedules. You should see skills and schedules from your connected repo's `cronfoundry.yaml`.

### 6. Trigger a manual run

1. Click "Run now" on any enabled schedule
2. You should be navigated to the run detail page
3. Watch events stream live (SSE live-tail)
4. Run should eventually reach `succeeded` or `partial_failure`

### 7. Check run history

Navigate to Runs. The manual run should appear with status, duration, and cost.

## If Something Goes Wrong

Check Container App logs:

```bash
az containerapp logs show \
  --name cfprod-api \
  --resource-group rg-cronfoundry-prod \
  --follow
```

Check runner job execution history:

```bash
az containerapp job execution list \
  --name cfprod-runner \
  --resource-group rg-cronfoundry-prod
```
```

- [ ] **Step 3: Commit**

```bash
git add deploy/README.md docs/guides/smoke-test-p4.md
git commit -m "docs: Azure deployment guide and P4 smoke test runbook"
```

---

## Task 7: Add Bicep build check to CI

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add Bicep validation step to ci.yml**

Add after the Docker build step in `.github/workflows/ci.yml`:

```yaml
      - name: Validate Bicep template
        run: |
          az bicep install
          az bicep build -f deploy/main.bicep --stdout > /dev/null
          echo "Bicep template is valid"
```

- [ ] **Step 2: Verify YAML syntax**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "YAML valid"
```

Expected: `YAML valid`

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add Bicep syntax validation to PR gate"
```

---

## Task 8: Final verification

- [ ] **Step 1: Run all Go tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Build the full binary**

```bash
cd /home/tng/workspace/cronfoundry && make build
```

Expected: both `cronfoundry` and `cronfoundry-runner` built.

- [ ] **Step 3: Build Docker image**

```bash
cd /home/tng/workspace/cronfoundry && docker build -f deploy/Dockerfile -t cronfoundry:p4 .
docker run --rm cronfoundry:p4 --help
```

Expected: help text printed.

- [ ] **Step 4: Validate Bicep**

```bash
az bicep build -f deploy/main.bicep --stdout > /dev/null && echo "Bicep valid"
```

Expected: `Bicep valid`

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "chore(p4): final build and Bicep validation passing"
```
