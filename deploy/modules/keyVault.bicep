param location string = resourceGroup().location
param name string
param cfServePrincipalId string
@description('Enable purge protection. Irrevocable once true — prevents re-deploy with same vault name for softDeleteRetentionDays. Use true for prod only.')
param enablePurgeProtection bool = false
param softDeleteRetentionDays int = 7
@secure()
@description('GitHub App private key (PEM). Seeded into the vault as secret "github-app-pem" so the serve Container App can resolve its Key Vault secret reference at creation time. Pass empty string only if you plan to az keyvault secret set before the Container App is deployed.')
param githubAppPem string = ''

// Key Vault Secrets User built-in role
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
    softDeleteRetentionInDays: softDeleteRetentionDays
    enablePurgeProtection: enablePurgeProtection
    // Public network access is intentional for MVP (Container Apps outbound IPs are dynamic).
    // Harden by adding networkAcls with default-deny or setting publicNetworkAccess: 'Disabled'
    // with a private endpoint once VNet integration is wired.
  }
}

resource kvServeRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(kv.id, cfServePrincipalId, kvSecretsUserRoleId)
  scope: kv
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', kvSecretsUserRoleId)
    principalId: cfServePrincipalId
    principalType: 'ServicePrincipal'
  }
}

// Pre-seed the github-app-pem secret so the serve Container App's
// Key Vault secret reference resolves at deploy time. Skipped when
// githubAppPem is empty; the operator must az keyvault secret set
// before the Container App is deployed in that case.
resource githubAppPemSecret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (!empty(githubAppPem)) {
  parent: kv
  name: 'github-app-pem'
  properties: {
    value: githubAppPem
    contentType: 'application/x-pem-file'
  }
}

output kvUrl string = kv.properties.vaultUri
output kvName string = kv.name
