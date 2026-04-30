// Package server persists and retrieves secrets under envelope
// encryption. The SecretStore interface is the single external contract.
// Implementations: EnvelopePostgresStore (postgres.go) for self-hosted
// Postgres, and azurekv.KeyVaultStore (sibling package) for Azure Key Vault.
package server

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get when a secret with the given name does not
// exist.
var ErrNotFound = errors.New("secretstore: not found")

// SecretStore reads, writes, and deletes secret values for a single
// organization. Callers never see ciphertext, nonces, or DEKs.
type SecretStore interface {
	// Get returns the cleartext value of the named secret.
	Get(ctx context.Context, name string) (string, error)

	// Put stores a cleartext value under the given name, overwriting any
	// prior version.
	Put(ctx context.Context, name, value string) error

	// Delete removes the secret. Returns nil if the secret does not exist.
	Delete(ctx context.Context, name string) error

	// List returns the names of all secrets, sorted alphabetically.
	// Values are never returned; this is a metadata-only operation.
	List(ctx context.Context) ([]string, error)
}
