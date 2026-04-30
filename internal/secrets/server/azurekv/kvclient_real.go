package azurekv

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// realKVClient wraps azsecrets.Client to implement KVClient.
type realKVClient struct {
	c *azsecrets.Client
}

// NewRealKVClient builds a KVClient backed by a live azsecrets.Client.
// vaultURL is e.g. "https://my-vault.vault.azure.net/".
func NewRealKVClient(vaultURL string, cred azcore.TokenCredential) (KVClient, error) {
	c, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azsecrets.NewClient(%q): %w", vaultURL, err)
	}
	return &realKVClient{c: c}, nil
}

func (r *realKVClient) GetSecret(ctx context.Context, name, version string) (string, error) {
	resp, err := r.c.GetSecret(ctx, name, version, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 404 {
			return "", ErrSecretNotFound
		}
		return "", err
	}
	if resp.Value == nil {
		return "", ErrSecretNotFound
	}
	return *resp.Value, nil
}

func (r *realKVClient) SetSecret(ctx context.Context, name, value string) error {
	params := azsecrets.SetSecretParameters{Value: &value}
	_, err := r.c.SetSecret(ctx, name, params, nil)
	return err
}

func (r *realKVClient) DeleteSecret(ctx context.Context, name string) error {
	_, err := r.c.DeleteSecret(ctx, name, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 404 {
			return nil
		}
		return err
	}
	return nil
}

func (r *realKVClient) ListSecrets(ctx context.Context) ([]string, error) {
	pager := r.c.NewListSecretPropertiesPager(nil)
	var names []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Value {
			if item.ID != nil {
				names = append(names, item.ID.Name())
			}
		}
	}
	return names, nil
}
