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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type runDisplayRow struct {
	id           string
	scheduleName string
	skillPath    string
	status       string
	started      string
	duration     string
}

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

	var display []runDisplayRow

	if scheduleName == "" {
		rows, err := q.ListRunsForOrg(ctx, dbgen.ListRunsForOrgParams{
			OrgID: org.ID,
			Limit: int32(limit),
		})
		if err != nil {
			return fmt.Errorf("list: %w", err)
		}
		for _, r := range rows {
			display = append(display, toRunDisplayRow(r.ID, r.Owner, r.RepoName, r.ScheduleName, r.SkillPath, r.Status, r.StartedAt, r.DurationMs))
		}
	} else {
		rows, err := q.ListRunsForSchedule(ctx, dbgen.ListRunsForScheduleParams{
			OrgID: org.ID,
			Name:  scheduleName,
			Limit: int32(limit),
		})
		if err != nil {
			return fmt.Errorf("list: %w", err)
		}
		for _, r := range rows {
			display = append(display, toRunDisplayRow(r.ID, r.Owner, r.RepoName, r.ScheduleName, r.SkillPath, r.Status, r.StartedAt, r.DurationMs))
		}
	}

	if len(display) == 0 {
		fmt.Fprintln(out, "(no runs yet)")
		return nil
	}

	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tSCHEDULE\tSKILL\tSTATUS\tSTARTED\tDURATION")
	for _, r := range display {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.id, r.scheduleName, r.skillPath, r.status, r.started, r.duration)
	}
	return tw.Flush()
}

func toRunDisplayRow(id pgtype.UUID, owner, repoName, scheduleName, skillPath, status string, startedAt pgtype.Timestamptz, durationMs *int32) runDisplayRow {
	started := "-"
	if startedAt.Valid {
		started = startedAt.Time.Format("2006-01-02 15:04:05")
	}
	duration := "-"
	if durationMs != nil {
		duration = fmt.Sprintf("%dms", *durationMs)
	}
	return runDisplayRow{
		id:           uuid.UUID(id.Bytes).String()[:8],
		scheduleName: owner + "/" + repoName + "/" + scheduleName,
		skillPath:    skillPath,
		status:       status,
		started:      started,
		duration:     duration,
	}
}
