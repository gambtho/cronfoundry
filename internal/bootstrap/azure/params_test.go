// internal/bootstrap/azure/params_test.go
package azure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteParams_AllFieldsPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "params.json")

	in := goodInputs()
	in.PEMContents = "-----BEGIN-----\nLINE1\nLINE2\n-----END-----\n"
	require.NoError(t, WriteParams(in, "MASTERKEYBASE64==", path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	want := []string{
		"env", "location", "imageTag",
		"githubAppId", "githubAppOAuthClientId", "githubAppOAuthClientSecret",
		"postgresAdminPassword", "masterKey", "githubAppPem",
		"adminLogins", "viewerLogins", "ingressExternal",
	}
	for _, k := range want {
		_, ok := doc.Parameters[k]
		require.True(t, ok, "missing param %q", k)
	}

	require.Equal(t, true, doc.Parameters["ingressExternal"].Value)
	require.Equal(t, "MASTERKEYBASE64==", doc.Parameters["masterKey"].Value)
	require.Equal(t, in.PEMContents, doc.Parameters["githubAppPem"].Value)
	require.Equal(t, "prod", doc.Parameters["env"].Value)
}
