package azure_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/secretstore"
	azuresecretstore "github.com/gambtho/cronfoundry/internal/secretstore/azure"
)

type fakeKVClient struct {
	secrets map[string]string
}

func (f *fakeKVClient) GetSecret(ctx context.Context, name, version string, opts interface{}) (string, error) {
	v, ok := f.secrets[name]
	if !ok {
		return "", azuresecretstore.ErrSecretNotFound
	}
	return v, nil
}

func (f *fakeKVClient) SetSecret(ctx context.Context, name, value string, opts interface{}) error {
	f.secrets[name] = value
	return nil
}

func (f *fakeKVClient) DeleteSecret(ctx context.Context, name string, opts interface{}) error {
	delete(f.secrets, name)
	return nil
}

func (f *fakeKVClient) ListSecrets(ctx context.Context) ([]string, error) {
	names := make([]string, 0, len(f.secrets))
	for n := range f.secrets {
		names = append(names, n)
	}
	return names, nil
}

func TestKeyVaultStore_Get_ReturnsValue(t *testing.T) {
	client := &fakeKVClient{secrets: map[string]string{"MY_SECRET": "hello"}}
	store := azuresecretstore.NewKeyVaultStore(client)
	val, err := store.Get(context.Background(), "MY_SECRET")
	require.NoError(t, err)
	require.Equal(t, "hello", val)
}

func TestKeyVaultStore_Get_NotFound(t *testing.T) {
	client := &fakeKVClient{secrets: map[string]string{}}
	store := azuresecretstore.NewKeyVaultStore(client)
	_, err := store.Get(context.Background(), "MISSING")
	require.ErrorIs(t, err, secretstore.ErrNotFound)
}

func TestKeyVaultStore_Put_And_Get(t *testing.T) {
	client := &fakeKVClient{secrets: map[string]string{}}
	store := azuresecretstore.NewKeyVaultStore(client)
	require.NoError(t, store.Put(context.Background(), "K", "V"))
	val, err := store.Get(context.Background(), "K")
	require.NoError(t, err)
	require.Equal(t, "V", val)
}

func TestKeyVaultStore_Delete(t *testing.T) {
	client := &fakeKVClient{secrets: map[string]string{"K": "V"}}
	store := azuresecretstore.NewKeyVaultStore(client)
	require.NoError(t, store.Delete(context.Background(), "K"))
	_, err := store.Get(context.Background(), "K")
	require.ErrorIs(t, err, secretstore.ErrNotFound)
}

func TestKeyVaultStore_List_Sorted(t *testing.T) {
	client := &fakeKVClient{secrets: map[string]string{"B": "2", "A": "1", "C": "3"}}
	store := azuresecretstore.NewKeyVaultStore(client)
	names, err := store.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"A", "B", "C"}, names)
	require.True(t, sort.StringsAreSorted(names))
}
