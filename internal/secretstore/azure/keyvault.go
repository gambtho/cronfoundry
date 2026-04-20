package azure

import (
	"context"
	"errors"
	"sort"

	"github.com/gambtho/cronfoundry/internal/secretstore"
)

// KeyVaultStore implements secretstore.SecretStore backed by Azure Key Vault.
type KeyVaultStore struct {
	client KVClient
}

// NewKeyVaultStore wraps a KVClient as a SecretStore. Panics if client is nil.
func NewKeyVaultStore(client KVClient) *KeyVaultStore {
	if client == nil {
		panic("secretstore/azure: NewKeyVaultStore: client must not be nil")
	}
	return &KeyVaultStore{client: client}
}

// Compile-time interface check.
var _ secretstore.SecretStore = (*KeyVaultStore)(nil)

func (s *KeyVaultStore) Get(ctx context.Context, name string) (string, error) {
	val, err := s.client.GetSecret(ctx, name, "")
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return "", secretstore.ErrNotFound
		}
		return "", err
	}
	return val, nil
}

func (s *KeyVaultStore) Put(ctx context.Context, name, value string) error {
	return s.client.SetSecret(ctx, name, value)
}

func (s *KeyVaultStore) Delete(ctx context.Context, name string) error {
	err := s.client.DeleteSecret(ctx, name)
	if errors.Is(err, ErrSecretNotFound) {
		return nil
	}
	return err
}

func (s *KeyVaultStore) List(ctx context.Context) ([]string, error) {
	names, err := s.client.ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
