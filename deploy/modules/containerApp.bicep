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
@secure()
param oauthClientSecret string
@secure()
param masterKey string
param githubAppPemSecretName string = 'github-app-pem'
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
      secrets: [
        { name: 'master-key', value: masterKey }
        { name: 'oauth-client-secret', value: oauthClientSecret }
        {
          name: 'github-app-pem'
          keyVaultUrl: '${kvUrl}secrets/${githubAppPemSecretName}'
          identity: cfServeIdentityId
        }
      ]
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
            { name: 'CRONFOUNDRY_DATABASE_URL', value: databaseUrl }
            { name: 'CRONFOUNDRY_GITHUB_APP_ID', value: githubAppId }
            { name: 'CRONFOUNDRY_GITHUB_APP_PEM', secretRef: 'github-app-pem' }
            { name: 'CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID', value: oauthClientId }
            { name: 'CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET', secretRef: 'oauth-client-secret' }
            { name: 'CRONFOUNDRY_MASTER_KEY', secretRef: 'master-key' }
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
        // maxReplicas is 1 until leader election / distributed locking is added to the scheduler.
        // Two replicas without advisory locks can double-dispatch the same run_id.
        minReplicas: 1
        maxReplicas: 1
      }
    }
  }
}

output fqdn string = app.properties.configuration.ingress.fqdn ?? ''
