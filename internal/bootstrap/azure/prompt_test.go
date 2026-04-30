// internal/bootstrap/azure/prompt_test.go
package azure

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrompt_HappyPath(t *testing.T) {
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "app.pem")
	require.NoError(t, os.WriteFile(pemPath,
		[]byte("-----BEGIN-----\nABCDEFGHIJKLMNOP\n-----END-----\n"), 0o600))

	stdin := strings.NewReader(strings.Join([]string{
		"",                // env (default prod)
		"",                // region (default swedencentral)
		"",                // image owner (default gambtho)
		"0.7.0",           // image tag
		"12345",           // github app id
		"Iv23liabc",       // oauth client id
		"super-secret",    // oauth client secret
		pemPath,           // pem path
		"alice",           // admin login
		"",                // postgres password (blank => generate)
	}, "\n") + "\n")

	var stdout bytes.Buffer
	in, err := Prompt(context.Background(), stdin, &stdout)
	require.NoError(t, err)
	require.Equal(t, "prod", in.Env)
	require.Equal(t, "swedencentral", in.Region)
	require.Equal(t, "gambtho", in.ImageOwner)
	require.Equal(t, "0.7.0", in.ImageTag)
	require.Contains(t, in.PEMContents, "BEGIN")
	require.Equal(t, "alice", in.AdminLogins)
	require.GreaterOrEqual(t, len(in.PostgresPassword), 20)
}
