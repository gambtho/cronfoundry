package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminConnectRepoCmd() *cobra.Command {
	var installationID int64
	var defaultBranch string
	var syncInterval int

	cmd := &cobra.Command{
		Use:   "connect-repo <owner/name>",
		Short: "Add (or update) a GitHub repo connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminConnectRepo(cmd.Context(), args[0], installationID, defaultBranch, syncInterval, os.Stdout)
		},
	}
	cmd.Flags().Int64Var(&installationID, "installation-id", 0, "GitHub App installation ID (required)")
	cmd.Flags().StringVar(&defaultBranch, "branch", "main", "default branch to poll")
	cmd.Flags().IntVar(&syncInterval, "sync-interval-sec", 60, "seconds between sync polls")
	_ = cmd.MarkFlagRequired("installation-id")
	return cmd
}

func runAdminConnectRepo(ctx context.Context, repo string, installID int64, branch string, syncSec int, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return fmt.Errorf("repo must be owner/name; got %q", repo)
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
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no organization seeded; run `cronfoundry admin init` first: %w", err)
		}
		return fmt.Errorf("load organization: %w", err)
	}

	row, err := q.InsertRepoConnection(ctx, dbgen.InsertRepoConnectionParams{
		OrgID:              org.ID,
		GithubAppInstallID: installID,
		Owner:              owner,
		Name:               name,
		DefaultBranch:      branch,
		SyncIntervalSec:    int32(syncSec),
	})
	if err != nil {
		return fmt.Errorf("insert repo connection: %w", err)
	}

	fmt.Fprintf(out, "Connected %s/%s (install=%d, branch=%s, interval=%ds)\n",
		row.Owner, row.Name, row.GithubAppInstallID, row.DefaultBranch, row.SyncIntervalSec)
	return nil
}
