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
