package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminShowRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show-run <run-id>",
		Short: "Show detail of a single run plus its last 20 events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminShowRun(cmd.Context(), args[0], os.Stdout)
		},
	}
}

func runAdminShowRun(ctx context.Context, runIDStr string, out io.Writer) error {
	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return fmt.Errorf("invalid run id: %w", err)
	}

	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	run, err := q.GetRunForAdmin(ctx, pgtype.UUID{Bytes: runID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("run not found: %s", runIDStr)
		}
		return fmt.Errorf("load run: %w", err)
	}

	fmt.Fprintf(out, "Run ID:         %s\n", uuid.UUID(run.ID.Bytes))
	fmt.Fprintf(out, "Status:         %s\n", run.Status)
	fmt.Fprintf(out, "Fire reason:    %s\n", run.FireReason)
	fmt.Fprintf(out, "Schedule:       %s/%s/%s\n", run.Owner, run.RepoName, run.ScheduleName)
	fmt.Fprintf(out, "Skill:          %s @ %s\n", run.SkillPath, run.SkillSha)
	fmt.Fprintf(out, "Cron:           %s\n", run.Cron)
	if run.Actor != nil {
		fmt.Fprintf(out, "Actor:          %s\n", *run.Actor)
	}
	if run.StartedAt.Valid {
		fmt.Fprintf(out, "Started:        %s\n", run.StartedAt.Time.Format(time.RFC3339))
	}
	if run.FinishedAt.Valid {
		fmt.Fprintf(out, "Finished:       %s\n", run.FinishedAt.Time.Format(time.RFC3339))
	}
	if run.DurationMs != nil {
		fmt.Fprintf(out, "Duration:       %dms\n", *run.DurationMs)
	}
	if run.TokensIn != nil {
		fmt.Fprintf(out, "Tokens in:      %d\n", *run.TokensIn)
	}
	if run.TokensOut != nil {
		fmt.Fprintf(out, "Tokens out:     %d\n", *run.TokensOut)
	}
	if run.CostCents != nil {
		fmt.Fprintf(out, "Cost (cents):   %d\n", *run.CostCents)
	}
	if run.WritebackCommitSha != nil {
		fmt.Fprintf(out, "Writeback SHA:  %s\n", *run.WritebackCommitSha)
	}
	if run.ErrorKind != nil && *run.ErrorKind != "" {
		fmt.Fprintf(out, "Error kind:     %s\n", *run.ErrorKind)
	}
	if run.ErrorMsg != nil && *run.ErrorMsg != "" {
		fmt.Fprintf(out, "Error message:  %s\n", *run.ErrorMsg)
	}

	events, err := q.ListRunEvents(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}
	if len(events) == 0 {
		fmt.Fprintln(out, "\n(no events recorded)")
		return nil
	}
	fmt.Fprintln(out, "\nEvents:")
	start := 0
	if len(events) > 20 {
		start = len(events) - 20
	}
	for _, ev := range events[start:] {
		fmt.Fprintf(out, "  [%s] %-5s %s: %s\n",
			ev.Ts.Time.Format("15:04:05"),
			ev.Level,
			ev.EventType,
			string(ev.PayloadJson))
	}
	return nil
}
