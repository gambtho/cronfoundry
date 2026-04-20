package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminListRunsCmd() *cobra.Command {
	var limit int
	var scheduleName string
	cmd := &cobra.Command{
		Use:   "list-runs",
		Short: "List recent runs (optionally filtered by schedule name)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListRuns(cmd.Context(), limit, scheduleName, os.Stdout)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "max number of rows to return")
	cmd.Flags().StringVar(&scheduleName, "schedule", "", "filter by schedule name")
	return cmd
}

func runAdminListRuns(ctx context.Context, limit int, scheduleName string, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	if limit <= 0 {
		limit = 20
	}
	const maxLimit = 1000
	if limit > maxLimit {
		limit = maxLimit
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
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

	var rows []dbgen.ListRunsForOrgRow
	if scheduleName == "" {
		rows, err = q.ListRunsForOrg(ctx, dbgen.ListRunsForOrgParams{
			OrgID: org.ID,
			Limit: int32(limit),
		})
		if err != nil {
			return fmt.Errorf("list: %w", err)
		}
	} else {
		sRows, err2 := q.ListRunsForSchedule(ctx, dbgen.ListRunsForScheduleParams{
			OrgID: org.ID,
			Name:  scheduleName,
			Limit: int32(limit),
		})
		if err2 != nil {
			return fmt.Errorf("list: %w", err2)
		}
		rows = make([]dbgen.ListRunsForOrgRow, 0, len(sRows))
		for _, r := range sRows {
			rows = append(rows, dbgen.ListRunsForOrgRow{
				ID: r.ID, Status: r.Status, FireReason: r.FireReason,
				Actor: r.Actor, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
				DurationMs: r.DurationMs, ErrorKind: r.ErrorKind, ErrorMsg: r.ErrorMsg,
				CreatedAt: r.CreatedAt, ScheduleName: r.ScheduleName,
				SkillPath: r.SkillPath, Owner: r.Owner, RepoName: r.RepoName,
			})
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "(no runs yet)")
		return nil
	}

	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tSCHEDULE\tSKILL\tSTATUS\tSTARTED\tDURATION")
	for _, r := range rows {
		started := "-"
		if r.StartedAt.Valid {
			started = r.StartedAt.Time.Format("2006-01-02 15:04:05")
		}
		duration := "-"
		if r.DurationMs != nil {
			duration = fmt.Sprintf("%dms", *r.DurationMs)
		}
		fmt.Fprintf(tw, "%s\t%s/%s\t%s\t%s\t%s\t%s\n",
			uuid.UUID(r.ID.Bytes).String()[:8],
			r.Owner, r.RepoName+"/"+r.ScheduleName,
			r.SkillPath,
			r.Status,
			started, duration)
	}
	return tw.Flush()
}
