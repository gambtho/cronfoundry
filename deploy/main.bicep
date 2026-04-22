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
param adminLogins string
param viewerLogins string = ''
param ingressExternal bool = false

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
    ingressExternal: ingressExternal
  }
}

output resourceGroup string = rgName
output kvUrl string = kv.outputs.kvUrl
output serveUrl string = serve.outputs.fqdn
