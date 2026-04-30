// Package azurekv provides an Azure Key Vault implementation of server.SecretStore.
package azurekv

import (
	"context"
	"errors"
)

// ErrSecretNotFound is returned by KVClient when the secret name doesn't exist.
var ErrSecretNotFound = errors.New("azure: secret not found")

// KVClient is the testability seam over azsecrets.Client.
// The real implementation wraps azsecrets.Client; tests use fakeKVClient.
type KVClient interface {
	// GetSecret returns the value of the named secret. Pass version "" to retrieve the latest.
	GetSecret(ctx context.Context, name, version string) (string, error)
	SetSecret(ctx context.Context, name, value string) error
	DeleteSecret(ctx context.Context, name string) error
	ListSecrets(ctx context.Context) ([]string, error)
}
