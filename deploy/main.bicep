// Subscription-scoped deployment.
// Deploy: az deployment sub create --name main -l eastus -f deploy/main.bicep -p deploy/params.json
// Or:     azd up
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
@secure()
param masterKey string
@secure()
@description('Contents of the GitHub App private key PEM file. Seeded into Key Vault as secret "github-app-pem". Pass "" to skip (operator must then upload before the Container App is created).')
param githubAppPem string = ''
param adminLogins string
param viewerLogins string = ''
param ingressExternal bool = false

@description('Honor the leftmost X-Forwarded-For header for client IP. Set true behind a reverse proxy / Container Apps ingress so per-IP rate limits track real clients, not the proxy IP.')
param trustProxy bool = false

@description('Externally-reachable base URL of the service (scheme+host, e.g. https://cronfoundry.example.com). Required in production for the CSRF Origin/Referer allowlist; empty disables the Origin check (dev only).')
param publicBaseUrl string = ''

var prefix = 'cf'
var rgName = 'rg-cronfoundry-${env}'

resource rg 'Microsoft.Resources/resourceGroups@2023-07-01' = {
  name: rgName
  location: location
}

module law 'br/public:avm/res/operational-insights/workspace:0.9.0' = {
  scope: rg
  name: 'law'
  params: {
    name: '${prefix}-law-${env}'
    location: location
    dataRetention: 30
  }
}

module identities 'modules/identities.bicep' = {
  scope: rg
  name: 'identities'
  params: {
    location: location
    prefix: prefix
  }
}

module kv 'modules/keyVault.bicep' = {
  scope: rg
  name: 'keyVault'
  params: {
    location: location
    name: '${prefix}-kv-${env}'
    cfServePrincipalId: identities.outputs.cfServePrincipalId
    // Some subscriptions (notably Microsoft-internal tenants) enforce
    // Key Vault purge protection via Azure Policy. Leaving this at the
    // module default (false) causes Azure to reject the deploy with
    // "enablePurgeProtection cannot be set to false." Cost: a deleted
    // vault is soft-preserved for softDeleteRetentionDays (7), so
    // re-deploys with the same vault name must wait out that window or
    // use a different env suffix.
    enablePurgeProtection: true
    githubAppPem: githubAppPem
  }
}

module pg 'modules/postgres.bicep' = {
  scope: rg
  name: 'postgres'
  params: {
    location: location
    serverName: '${prefix}-pg-${env}'
    adminPassword: postgresAdminPassword
    // subnetId/privateDnsZoneId are empty for the initial public-networking deploy.
    // Add VNet integration by wiring a vnet module and passing real subnet/DNS IDs.
  }
}

module cae 'modules/containerAppsEnv.bicep' = {
  scope: rg
  name: 'cae'
  params: {
    location: location
    name: '${prefix}-cae-${env}'
    logAnalyticsWorkspaceName: law.outputs.name
  }
}

// DSN uses the admin username surfaced from the postgres module to avoid duplication.
var pgDsn = 'postgres://${pg.outputs.adminUser}:${postgresAdminPassword}@${pg.outputs.fqdn}/cronfoundry?sslmode=require'
var runnerJobName = '${prefix}-runner-${env}'

// serve deploys first (runner depends on serve.outputs.fqdn); serve uses runnerJobName var
// directly so it does not depend on runner's output — no circular dependency.
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
    apiBaseUrl: ingressExternal
      ? 'https://${serve.outputs.fqdn}'
      : 'http://${serve.outputs.fqdn}'
  }
}

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
    oauthClientSecret: githubAppOAuthClientSecret
    masterKey: masterKey
    adminLogins: adminLogins
    viewerLogins: viewerLogins
    kvUrl: kv.outputs.kvUrl
    azureSubscriptionId: subscription().subscriptionId
    azureResourceGroup: rgName
    azureCaeJobName: runnerJobName
    caeDefaultDomain: cae.outputs.defaultDomain
    ingressExternal: ingressExternal
    trustProxy: trustProxy
    publicBaseUrl: publicBaseUrl
  }
}

// The serve identity needs Microsoft.App/jobs/start/action on the runner job.
// Contributor (b24988ac-6180-42a0-ab88-20f7382dd24c) includes it; scoped to RG.
module serveJobStartRole 'modules/roleAssignment.bicep' = {
  scope: rg
  name: 'serveJobStartRole'
  params: {
    principalId: identities.outputs.cfServePrincipalId
    roleDefinitionId: 'b24988ac-6180-42a0-ab88-20f7382dd24c'
    principalType: 'ServicePrincipal'
  }
}

output resourceGroup string = rgName
output kvUrl string = kv.outputs.kvUrl
output serveUrl string = serve.outputs.fqdn
