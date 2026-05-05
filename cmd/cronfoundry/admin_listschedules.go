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

func newAdminListSchedulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-schedules",
		Short: "List all schedules discovered from connected repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListSchedules(cmd.Context(), os.Stdout)
		},
	}
}

func runAdminListSchedules(ctx context.Context, out io.Writer) error {
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
	rows, err := q.ListSchedulesByOrg(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}

	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "(no schedules discovered yet)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "REPO\tSKILL\tSCHEDULE\tCRON\tPROVIDER\tENABLED")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s/%s\t%s\t%s\t%s\t%s\t%t\n",
			r.Owner, r.RepoName, r.SkillPath, r.Name, r.Cron, r.Provider, r.Enabled)
	}
	return tw.Flush()
}
