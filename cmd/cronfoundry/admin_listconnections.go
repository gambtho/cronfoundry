package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminListConnectionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-connections",
		Short: "List connected GitHub repos and their last sync state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListConnections(cmd.Context(), os.Stdout)
		},
	}
}

func runAdminListConnections(ctx context.Context, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no organization seeded; run `cronfoundry admin init` first: %w", err)
		}
		return fmt.Errorf("load organization: %w", err)
	}
	rows, err := q.ListRepoConnections(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}

	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "(no connected repos)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REPO\tBRANCH\tINSTALL\tINTERVAL\tLAST SYNC\tSTATE")
	for _, r := range rows {
		sync := "never"
		if r.LastSyncedAt.Valid {
			sync = r.LastSyncedAt.Time.Format("2006-01-02 15:04:05")
		}
		state := "ok"
		if r.LastSyncError != nil && *r.LastSyncError != "" {
			state = "ERROR"
		}
		_, _ = fmt.Fprintf(tw, "%s/%s\t%s\t%d\t%ds\t%s\t%s\n",
			r.Owner, r.Name, r.DefaultBranch, r.GithubAppInstallID, r.SyncIntervalSec, sync, state)
	}
	return tw.Flush()
}
