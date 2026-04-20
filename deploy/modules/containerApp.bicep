param location string = resourceGroup().location
param name string
param environmentId string
param imageTag string = 'latest'
param cfServeIdentityId string
param cfServeClientId string
param ingressExternal bool = false
param databaseUrl string
param githubAppId string
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
            { name: 'CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID', value: oauthClientId }
            { name: 'CRONFOUNDRY_ADMIN_LOGINS', value: adminLogins }
            { name: 'CRONFOUNDRY_VIEWER_LOGINS', value: viewerLogins }
            { name: 'AZURE_KEYVAULT_URL', value: kvUrl }
            { name: 'AZURE_SUBSCRIPTION_ID', value: azureSubscriptionId }
            { name: 'AZURE_CAE_RESOURCE_GROUP', value: azureResourceGroup }
            { name: 'AZURE_CAE_JOB_NAME', value: azureCaeJobName }
            { name: 'AZURE_CLIENT_ID', value: cfServeClientId }
            { name: 'DISPATCHER', value: 'azure' }
            { name: 'SECRET_STORE', value: 'keyvault' }
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
