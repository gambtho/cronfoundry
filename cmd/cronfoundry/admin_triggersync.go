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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/sync"
)

const (
	envGitHubAppID     = "CRONFOUNDRY_GITHUB_APP_ID"
	envGitHubAppPEM    = "CRONFOUNDRY_GITHUB_APP_PEM"
	envGitHubBaseURL   = "CRONFOUNDRY_GITHUB_BASE_URL"
	envGitHubCloneBase = "CRONFOUNDRY_GITHUB_CLONE_BASE"
)

func newAdminTriggerSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trigger-sync <owner/name>",
		Short: "Force an immediate sync pass on a connected repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminTriggerSync(cmd.Context(), args[0], os.Stdout)
		},
	}
}

func runAdminTriggerSync(ctx context.Context, repo string, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	appID := os.Getenv(envGitHubAppID)
	if appID == "" {
		return fmt.Errorf("%s is required", envGitHubAppID)
	}
	pemPath := os.Getenv(envGitHubAppPEM)
	if pemPath == "" {
		return fmt.Errorf("%s is required (path to GitHub App private key PEM)", envGitHubAppPEM)
	}
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", envGitHubAppPEM, err)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return fmt.Errorf("repo must be owner/name; got %q", repo)
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
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

	// Find the connection by owner/name.
	rows, err := q.ListRepoConnections(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("list connections: %w", err)
	}
	var connID pgtype.UUID
	for _, r := range rows {
		if r.Owner == owner && r.Name == name {
			connID = r.ID
			break
		}
	}
	if !connID.Valid {
		return fmt.Errorf("no connection for %s/%s; run `cronfoundry admin connect-repo` first", owner, name)
	}

	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      appID,
		PrivateKey: pemBytes,
	})
	poller := sync.NewPoller(sync.PollerConfig{
		Pool:          pool,
		OrgID:         org.ID,
		Installations: cache,
	})

	if err := poller.SyncOne(ctx, connID); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	fmt.Fprintf(out, "Synced %s/%s\n", owner, name)
	return nil
}
