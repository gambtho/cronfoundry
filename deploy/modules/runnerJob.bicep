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
          command: ['/runner']
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
