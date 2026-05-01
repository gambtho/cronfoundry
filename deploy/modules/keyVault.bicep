param location string = resourceGroup().location
param name string
param cfServePrincipalId string
@description('Enable purge protection. Irrevocable once true — prevents re-deploy with same vault name for softDeleteRetentionDays. Use true for prod only.')
param enablePurgeProtection bool = false
param softDeleteRetentionDays int = 7
@secure()
@description('GitHub App private key (PEM). Seeded into the vault as secret "github-app-pem" so the serve Container App can resolve its Key Vault secret reference at creation time. Pass empty string for the deploy-first flow: a placeholder is pre-seeded and the install script overwrites it via "az keyvault secret set" after the manifest flow.')
param githubAppPem string = ''

// Key Vault Secrets Officer — the serve app creates secrets via the web UI
var kvSecretsOfficerRoleId = 'b86a8fe4-44ce-4948-aee5-eccb2c155cd7'

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
  name: guid(kv.id, cfServePrincipalId, kvSecretsOfficerRoleId)
  scope: kv
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', kvSecretsOfficerRoleId)
    principalId: cfServePrincipalId
    principalType: 'ServicePrincipal'
  }
}

// Always pre-seed the github-app-pem secret so the serve Container App's
// Key Vault secret reference resolves at deploy time. When githubAppPem is
// empty (Phase 1 deploy-first flow), seed a placeholder; the install script
// overwrites it via `az keyvault secret set` after the manifest flow.
resource githubAppPemSecret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: kv
  name: 'github-app-pem'
  properties: {
    value: empty(githubAppPem) ? 'placeholder-set-after-manifest-flow' : githubAppPem
    contentType: 'application/x-pem-file'
  }
}

output kvUrl string = kv.properties.vaultUri
output kvName string = kv.name
