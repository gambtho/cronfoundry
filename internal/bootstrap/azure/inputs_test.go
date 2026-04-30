// internal/bootstrap/azure/inputs_test.go
package azure

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputs_Validate_OK(t *testing.T) {
	require.NoError(t, goodInputs().Validate())
}

func TestInputs_Validate_BadEnv(t *testing.T) {
	in := goodInputs()
	in.Env = "this-is-far-too-long-suffix"
	err := in.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "env")
}

func TestInputs_Validate_PasswordNotAlphanumeric(t *testing.T) {
	in := goodInputs()
	in.PostgresPassword = "Abc@123XyzAbc123XyzAbc"
	err := in.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "alphanumeric")
}

func TestInputs_Validate_MissingImageOwner(t *testing.T) {
	in := goodInputs()
	in.ImageOwner = ""
	err := in.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "image owner")
}

func TestInputs_Validate_MissingImageTag(t *testing.T) {
	in := goodInputs()
	in.ImageTag = ""
	err := in.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "image tag")
}

func TestGeneratePassword_AlphanumericAndLongEnough(t *testing.T) {
	p, err := GeneratePostgresPassword()
	require.NoError(t, err)
	require.Len(t, p, 24)
	for _, r := range p {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		require.True(t, ok, "non-alphanumeric in %q", p)
	}
}

func TestGeneratePassword_DistinctAcrossCalls(t *testing.T) {
	a, err := GeneratePostgresPassword()
	require.NoError(t, err)
	b, err := GeneratePostgresPassword()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func goodInputs() Inputs {
	return Inputs{
		Env:               "prod",
		Region:            "swedencentral",
		ImageOwner:        "gambtho",
		ImageTag:          "0.7.0",
		GithubAppID:       "12345",
		OAuthClientID:     "Iv23liabc",
		OAuthClientSecret: "shhh",
		PEMContents:       strings.Repeat("x", 32),
		AdminLogins:       "alice",
		PostgresPassword:  "Abc123XyzAbc123XyzAbc",
	}
}
