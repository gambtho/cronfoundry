// Subscription-scoped deployment.
// Deploy: az deployment sub create -l eastus -f deploy/main.bicep -p deploy/params.json
// Or:     azd up
targetScope = 'subscription'

param env string = 'prod'
param location string = 'eastus'
param imageTag string = 'latest'
param githubAppId string
@secure()
param githubAppOAuthClientId string
@secure()
param postgresAdminPassword string
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
    // Subnet and private DNS zone IDs are empty for initial deploy without VNet.
    // Add VNet integration in a follow-up by wiring a vnet module here.
    subnetId: ''
    privateDnsZoneId: ''
  }
}

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
