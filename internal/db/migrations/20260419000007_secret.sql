-- +goose Up
CREATE TABLE secret (
    id           uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       uuid NOT NULL,
    name         text NOT NULL,
    dek_wrapped  bytea NOT NULL,
    ciphertext   bytea NOT NULL,
    nonce        bytea NOT NULL,
    version      int NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (org_id, name)
);

-- +goose Down
DROP TABLE secret;
