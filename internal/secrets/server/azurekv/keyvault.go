package azurekv

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/gambtho/cronfoundry/internal/secrets/server"
)

// secretNameRe enforces Azure Key Vault secret name constraints: 1–127 alphanumeric/hyphen chars.
var secretNameRe = regexp.MustCompile(`^[0-9a-zA-Z-]{1,127}$`)

// ErrInvalidSecretName is returned when a secret name violates Azure Key Vault naming rules.
var ErrInvalidSecretName = errors.New("azure keyvault: secret name must match ^[0-9a-zA-Z-]{1,127}$")

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
var _ server.SecretStore = (*KeyVaultStore)(nil)

func (s *KeyVaultStore) Get(ctx context.Context, name string) (string, error) {
	val, err := s.client.GetSecret(ctx, name, "")
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return "", server.ErrNotFound
		}
		return "", err
	}
	return val, nil
}

func (s *KeyVaultStore) Put(ctx context.Context, name, value string) error {
	if !secretNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidSecretName, name)
	}
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
