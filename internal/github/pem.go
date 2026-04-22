package github

import (
	"fmt"
	"os"
	"strings"
)

// ReadPEM returns the PEM bytes for a GitHub App private key.
//
// If value starts with "-----BEGIN" (after trimming leading whitespace)
// it is treated as the inline contents of a PEM block — this is the form
// produced by Azure Container Apps when a Key Vault secret is mapped
// directly into an environment variable.
//
// Otherwise value is treated as a filesystem path and read with
// os.ReadFile — this is the local-dev / docker-compose form where the
// operator mounts a `.pem` file and points the env var at its path.
func ReadPEM(value string) ([]byte, error) {
	if strings.HasPrefix(strings.TrimLeft(value, " \t\r\n"), "-----BEGIN") {
		return []byte(value), nil
	}
	b, err := os.ReadFile(value)
	if err != nil {
		return nil, fmt.Errorf("read PEM file %q: %w", value, err)
	}
	return b, nil
}
