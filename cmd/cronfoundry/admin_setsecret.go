package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secretstore"
)

func newAdminSetSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-secret <name>",
		Short: "Store a secret value (reads one line from stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminSetSecret(cmd.Context(), args[0], os.Stdin, os.Stdout)
		},
	}
	return cmd
}

func runAdminSetSecret(ctx context.Context, name string, in io.Reader, out io.Writer) error {
	encodedMaster := os.Getenv(envMasterKey)
	if encodedMaster == "" {
		return fmt.Errorf("%s is required; run `cronfoundry admin init` first", envMasterKey)
	}
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}

	master, err := secretstore.ParseMasterKey(encodedMaster)
	if err != nil {
		return fmt.Errorf("parse %s: %w", envMasterKey, err)
	}

	value, err := readOneLine(in)
	if err != nil {
		return fmt.Errorf("read value from stdin: %w", err)
	}
	if value == "" {
		return fmt.Errorf("empty value; type the secret on stdin and press Enter")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("no organization seeded; run `cronfoundry admin init` first: %w", err)
	}

	store := secretstore.NewEnvelopePostgresStore(pool, org.ID, master)
	if err := store.Put(ctx, name, value); err != nil {
		return fmt.Errorf("put secret: %w", err)
	}
	fmt.Fprintf(out, "Stored secret %q\n", name)
	return nil
}

// readOneLine reads up to the first newline (stripping CR+LF) or EOF.
func readOneLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(strings.TrimRight(line, "\n"), "\r"), nil
}
