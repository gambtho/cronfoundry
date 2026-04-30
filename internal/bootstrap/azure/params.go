// internal/bootstrap/azure/params.go
package azure

import (
	"encoding/json"
	"os"
)

// WriteParams writes a Bicep deployment parameters file with every required
// field set, ingressExternal=true, and the PEM contents inlined.
func WriteParams(in Inputs, masterKey, paramsPath string) error {
	type p struct {
		Value any `json:"value"`
	}
	doc := map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": map[string]p{
			"env":                        {in.Env},
			"location":                   {in.Region},
			"imageTag":                   {in.ImageTag},
			"githubAppId":                {in.GithubAppID},
			"githubAppOAuthClientId":     {in.OAuthClientID},
			"githubAppOAuthClientSecret": {in.OAuthClientSecret},
			"postgresAdminPassword":      {in.PostgresPassword},
			"masterKey":                  {masterKey},
			"githubAppPem":               {in.PEMContents},
			"adminLogins":                {in.AdminLogins},
			"viewerLogins":               {""},
			"ingressExternal":            {true},
			"trustProxy":                 {true},
			"publicBaseUrl":              {""},
		},
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paramsPath, body, 0o600)
}
