package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// EnvelopePostgresStore is the P2 SecretStore implementation: each secret
// carries its own DEK, wrapped with the process-wide master key.
type EnvelopePostgresStore struct {
	pool   *pgxpool.Pool
	orgID  pgtype.UUID
	master []byte
}

// NewEnvelopePostgresStore constructs a store bound to a single org and
// master key. master must be exactly 32 bytes.
func NewEnvelopePostgresStore(pool *pgxpool.Pool, orgID pgtype.UUID, master []byte) *EnvelopePostgresStore {
	return &EnvelopePostgresStore{pool: pool, orgID: orgID, master: master}
}

// Compile-time interface check.
var _ SecretStore = (*EnvelopePostgresStore)(nil)

func (s *EnvelopePostgresStore) Get(ctx context.Context, name string) (string, error) {
	q := dbgen.New(s.pool)
	row, err := q.GetSecretByName(ctx, dbgen.GetSecretByNameParams{
		OrgID: s.orgID,
		Name:  name,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("secretstore: get %q: %w", name, err)
	}

	dek, err := unwrapDEK(s.master, row.DekWrapped)
	if err != nil {
		return "", fmt.Errorf("secretstore: unwrap DEK for %q: %w", name, err)
	}
	plaintext, err := decrypt(dek, row.Ciphertext, row.Nonce)
	if err != nil {
		return "", fmt.Errorf("secretstore: decrypt %q: %w", name, err)
	}

	// Best-effort touch: a failure to update last_used_at must not block a
	// successful Get. Metadata is cosmetic in P2.
	_ = q.TouchSecret(ctx, row.ID)
	return string(plaintext), nil
}

func (s *EnvelopePostgresStore) Put(ctx context.Context, name, value string) error {
	dek, wrapped, err := newWrappedDEK(s.master)
	if err != nil {
		return fmt.Errorf("secretstore: put %q: new DEK: %w", name, err)
	}
	ct, nonce, err := encrypt(dek, []byte(value))
	if err != nil {
		return fmt.Errorf("secretstore: put %q: encrypt: %w", name, err)
	}

	q := dbgen.New(s.pool)
	_, err = q.UpsertSecret(ctx, dbgen.UpsertSecretParams{
		OrgID:      s.orgID,
		Name:       name,
		DekWrapped: wrapped,
		Ciphertext: ct,
		Nonce:      nonce,
	})
	if err != nil {
		return fmt.Errorf("secretstore: put %q: %w", name, err)
	}
	return nil
}

func (s *EnvelopePostgresStore) Delete(ctx context.Context, name string) error {
	q := dbgen.New(s.pool)
	_, err := q.DeleteSecret(ctx, dbgen.DeleteSecretParams{
		OrgID: s.orgID,
		Name:  name,
	})
	if err != nil {
		return fmt.Errorf("secretstore: delete %q: %w", name, err)
	}
	return nil
}

func (s *EnvelopePostgresStore) List(ctx context.Context) ([]string, error) {
	q := dbgen.New(s.pool)
	rows, err := q.ListSecretNames(ctx, s.orgID)
	if err != nil {
		return nil, fmt.Errorf("secretstore: list: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out, nil
}
