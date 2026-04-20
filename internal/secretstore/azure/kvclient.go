// Package azure provides an Azure Key Vault implementation of secretstore.SecretStore.
package azure

import (
	"context"
	"errors"
)

// ErrSecretNotFound is returned by KVClient when the secret name doesn't exist.
var ErrSecretNotFound = errors.New("azure: secret not found")

// KVClient is the testability seam over azsecrets.Client.
// The real implementation wraps azsecrets.Client; tests use fakeKVClient.
type KVClient interface {
	GetSecret(ctx context.Context, name, version string, opts interface{}) (string, error)
	SetSecret(ctx context.Context, name, value string, opts interface{}) error
	DeleteSecret(ctx context.Context, name string, opts interface{}) error
	ListSecrets(ctx context.Context) ([]string, error)
}
