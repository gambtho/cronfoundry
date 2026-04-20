// cmd/cronfoundry/admin_init.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/db"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secretstore"
)

const envMasterKey = "CRONFOUNDRY_MASTER_KEY"
const envDatabaseURL = "CRONFOUNDRY_DATABASE_URL"

func newAdminInitCmd() *cobra.Command {
	var orgName string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Run migrations and seed the first organization row",
		Long: `Run all database migrations and seed the initial organization.

If CRONFOUNDRY_MASTER_KEY is unset, a fresh master key is generated, printed
to stdout, and the command exits without touching the database. The operator
must export the printed key and re-run ` + "`cronfoundry admin init`" + ` to
actually bootstrap.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminInit(cmd.Context(), orgName)
		},
	}
	cmd.Flags().StringVar(&orgName, "org-name", "default", "name for the seeded organization row")
	return cmd
}

func runAdminInit(ctx context.Context, orgName string) error {
	if os.Getenv(envMasterKey) == "" {
		key, err := secretstore.GenerateMasterKey()
		if err != nil {
			return fmt.Errorf("generate master key: %w", err)
		}
		fmt.Fprintln(os.Stdout, "CRONFOUNDRY_MASTER_KEY is not set. Generated one for you:")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, "  %s=%s\n", envMasterKey, key)
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Add this to your environment (do not lose it — it cannot be recovered)")
		fmt.Fprintln(os.Stdout, "and re-run `cronfoundry admin init`.")
		return nil
	}

	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	fmt.Fprintln(os.Stdout, "Running migrations...")
	if err := db.Migrate(ctx, dsn); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	existing, err := q.GetFirstOrganization(ctx)
	if err == nil {
		fmt.Fprintf(os.Stdout, "Organization already seeded (id=%s, name=%q). Ready.\n", existing.ID.String(), existing.Name)
		return nil
	}

	row, err := q.CreateOrganization(ctx, orgName)
	if err != nil {
		return fmt.Errorf("seed organization: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Seeded organization id=%s name=%q. Ready.\n", row.ID.String(), row.Name)
	return nil
}
