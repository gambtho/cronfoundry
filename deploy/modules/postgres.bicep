param location string = resourceGroup().location
param serverName string
param adminUser string = 'cfadmin'
@secure()
param adminPassword string
param subnetId string = ''
param privateDnsZoneId string = ''

var usePrivateNetwork = !empty(subnetId) && !empty(privateDnsZoneId)

resource pg 'Microsoft.DBforPostgreSQL/flexibleServers@2023-06-01-preview' = {
  name: serverName
  location: location
  sku: {
    name: 'Standard_B1ms'
    tier: 'Burstable'
  }
  properties: {
    administratorLogin: adminUser
    administratorLoginPassword: adminPassword
    version: '16'
    storage: {
      storageSizeGB: 32
    }
    network: usePrivateNetwork ? {
      delegatedSubnetResourceId: subnetId
      privateDnsZoneArmResourceId: privateDnsZoneId
    } : null
    backup: {
      backupRetentionDays: 7
      geoRedundantBackup: 'Disabled'
    }
    highAvailability: {
      mode: 'Disabled'
    }
  }
}

// When not using VNet integration, allow Azure-internal traffic (Container Apps, etc.)
resource allowAzure 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2023-06-01-preview' = if (!usePrivateNetwork) {
  parent: pg
  name: 'AllowAllAzureServicesAndResourcesWithinAzureIps'
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
}

resource cronfoundryDb 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-06-01-preview' = {
  parent: pg
  name: 'cronfoundry'
  properties: {
    charset: 'UTF8'
    collation: 'en_US.UTF8'
  }
}

output fqdn string = pg.properties.fullyQualifiedDomainName
output dbName string = cronfoundryDb.name
output adminUser string = adminUser
