package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/api"
	"github.com/gambtho/cronfoundry/internal/cloud"
	cloudazure "github.com/gambtho/cronfoundry/internal/cloud/azure"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/scheduler"
	"github.com/gambtho/cronfoundry/internal/secretstore"
	secretstoreazure "github.com/gambtho/cronfoundry/internal/secretstore/azure"
	"github.com/gambtho/cronfoundry/internal/sync"
	"github.com/gambtho/cronfoundry/internal/token"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

const (
	envOAuthClientID     = "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID"
	envOAuthClientSecret = "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET"
	envAdminLogins       = "CRONFOUNDRY_ADMIN_LOGINS"
	envViewerLogins      = "CRONFOUNDRY_VIEWER_LOGINS"
	envWebhookSecret     = "CRONFOUNDRY_GITHUB_WEBHOOK_SECRET"
)

func newServeCmd() *cobra.Command {
	var addr string
	var cadence time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the CronFoundry service: API + scheduler + sync loops",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), addr, cadence)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "API listen address")
	cmd.Flags().DurationVar(&cadence, "tick-cadence", 30*time.Second, "scheduler tick interval")
	return cmd
}

func runServe(ctx context.Context, addr string, cadence time.Duration) error {
	// --- Validate + load config from env ---
	masterEnc := os.Getenv(envMasterKey)
	if masterEnc == "" {
		return fmt.Errorf("%s is required", envMasterKey)
	}
	master, err := secretstore.ParseMasterKey(masterEnc)
	if err != nil {
		return fmt.Errorf("parse master key: %w", err)
	}
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	appID := os.Getenv(envGitHubAppID)
	pemPath := os.Getenv(envGitHubAppPEM)
	if appID == "" || pemPath == "" {
		return fmt.Errorf("%s and %s are required", envGitHubAppID, envGitHubAppPEM)
	}

	oauthClientID := os.Getenv(envOAuthClientID)
	oauthClientSecret := os.Getenv(envOAuthClientSecret)
	adminLoginsRaw := os.Getenv(envAdminLogins)
	runnerAPIURL := os.Getenv("CRONFOUNDRY_API_BASE_URL")
	if oauthClientID == "" {
		return fmt.Errorf("%s is required", envOAuthClientID)
	}
	if oauthClientSecret == "" {
		return fmt.Errorf("%s is required", envOAuthClientSecret)
	}
	if adminLoginsRaw == "" {
		return fmt.Errorf("%s must contain at least one login", envAdminLogins)
	}
	adminLogins := splitLogins(adminLoginsRaw)
	viewerLogins := splitLogins(os.Getenv(envViewerLogins))

	pemBytes, err := github.ReadPEM(pemPath)
	if err != nil {
		return fmt.Errorf("read PEM: %w", err)
	}

	// --- Open DB pool + load org ---
	setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	pool, err := pgxpool.New(setupCtx, dsn)
	setupCancel()
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("load organization (run `cronfoundry admin init`?): %w", err)
	}

	// Bootstrap: seed the app_user table from env vars on first startup only.
	// Once any user exists (from a prior bootstrap or a UI-driven admin add),
	// the env vars become inert — UI edits win. CreateUser itself is
	// ON CONFLICT DO NOTHING, so running this again is harmless, but the
	// count gate documents the write-once intent.
	if count, err := q.CountUsers(ctx, org.ID); err != nil {
		return fmt.Errorf("count users: %w", err)
	} else if count == 0 && (len(adminLogins) > 0 || len(viewerLogins) > 0) {
		adminsSeeded := 0
		viewersSeeded := 0
		seedOne := func(login, role string) bool {
			if _, err := q.CreateUser(ctx, dbgen.CreateUserParams{
				OrgID:       org.ID,
				GithubLogin: login,
				Role:        role,
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					// DO-NOTHING conflict — not an error, just already exists.
					return true
				}
				slog.Error("serve: bootstrap user failed", "login", login, "role", role, "err", err)
				return false
			}
			return true
		}
		for _, login := range adminLogins {
			if seedOne(login, "admin") {
				adminsSeeded++
			}
		}
		for _, login := range viewerLogins {
			if seedOne(login, "viewer") {
				viewersSeeded++
			}
		}
		slog.Info("serve: bootstrapped app_user from env",
			"admins_requested", len(adminLogins),
			"admins_seeded", adminsSeeded,
			"viewers_requested", len(viewerLogins),
			"viewers_seeded", viewersSeeded)
		// If admins were requested but none landed, the service would come
		// up with no admins and resolveRole would reject every login —
		// indistinguishable at the UI from a complete outage. Fail fast so
		// the operator sees the underlying DB error instead of a mysterious
		// "access denied" page.
		if len(adminLogins) > 0 && adminsSeeded == 0 {
			return fmt.Errorf("bootstrap: all %d admin seeds failed — refusing to start with zero admins", len(adminLogins))
		}
	}

	// --- Construct collaborators ---
	store, err := buildSecretStore(pool, org.ID, master)
	if err != nil {
		return fmt.Errorf("build secret store: %w", err)
	}
	signer := token.New(master)
	ghBaseURL := os.Getenv(envGitHubBaseURL)
	installsCfg := github.InstallationCacheConfig{
		AppID:      appID,
		PrivateKey: pemBytes,
	}
	if ghBaseURL != "" {
		installsCfg.BaseURL = ghBaseURL
	}
	installs := github.NewInstallationCache(installsCfg)

	poller := sync.NewPoller(sync.PollerConfig{
		Pool:          pool,
		OrgID:         org.ID,
		Installations: installs,
		GitHubBaseURL: ghBaseURL,
	})

	// --- Initial orphan sweep ---
	if n, err := scheduler.SweepOrphans(ctx, scheduler.Deps{Pool: pool}); err != nil {
		slog.Warn("serve: initial orphan sweep failed", "err", err)
	} else if n > 0 {
		slog.Info("serve: initial orphan sweep reclaimed runs", "count", n)
	}

	// --- Build API + scheduler ---
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	api.RegisterRoutes(mux, api.Deps{
		Pool:          pool,
		Signer:        signer,
		Secrets:       store,
		Installations: installs,
	})
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         master,
		OAuthClientID:     oauthClientID,
		OAuthClientSecret: oauthClientSecret,
		AdminLogins:       adminLogins,
		ViewerLogins:      viewerLogins,
		Queries:           q,
		Secrets:           store,
		APIBaseURL:        "http://" + addr,
		WebhookSecret:     []byte(os.Getenv(envWebhookSecret)),
		Syncer:            poller,
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self binary: %w", err)
	}
	dispatcher, err := buildJobDispatcher()
	if err != nil {
		return fmt.Errorf("build job dispatcher: %w", err)
	}
	schedDeps := scheduler.Deps{
		Pool:         pool,
		Signer:       signer,
		Dispatcher:   dispatcher,
		APIBaseURL:   "http://" + addr,
		RunnerAPIURL: runnerAPIURL,
		RunnerBinary: self,
	}

	// --- Signal-aware context ---
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 2)

	// API goroutine.
	go func() {
		slog.Info("serve: API listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("api: %w", err)
		}
	}()

	// Scheduler goroutine.
	go func() {
		if err := scheduler.Loop(ctx, cadence, schedDeps); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			errCh <- fmt.Errorf("scheduler: %w", err)
		}
	}()

	// Wait for shutdown signal or subsystem error.
	select {
	case err := <-errCh:
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		return err
	case <-ctx.Done():
		slog.Info("serve: shutdown signal received")
	}

	// Graceful shutdown: give the API 10s to drain.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("serve: api shutdown error", "err", err)
	}
	return nil
}

func splitLogins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// buildJobDispatcher returns a ContainerAppsJobDispatcher when
// AZURE_CAE_RESOURCE_GROUP, AZURE_CAE_JOB_NAME, and AZURE_SUBSCRIPTION_ID are all set;
// otherwise returns a SubprocessDispatcher for local use.
func buildJobDispatcher() (cloud.JobDispatcher, error) {
	rg := os.Getenv("AZURE_CAE_RESOURCE_GROUP")
	jobName := os.Getenv("AZURE_CAE_JOB_NAME")
	subID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	image := os.Getenv("AZURE_CAE_JOB_IMAGE")
	if rg == "" || jobName == "" || subID == "" {
		if rg != "" || jobName != "" || subID != "" {
			slog.Warn("serve: partial Azure dispatcher config — falling back to subprocess",
				"AZURE_CAE_RESOURCE_GROUP_set", rg != "",
				"AZURE_CAE_JOB_NAME_set", jobName != "",
				"AZURE_SUBSCRIPTION_ID_set", subID != "")
		}
		return cloud.NewSubprocessDispatcher(), nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	armClient, err := cloudazure.NewRealARMJobsClient(subID, cred)
	if err != nil {
		return nil, fmt.Errorf("arm jobs client: %w", err)
	}
	return cloudazure.NewContainerAppsJobDispatcher(armClient, rg, jobName, image), nil
}

// buildSecretStore returns a KeyVaultStore when AZURE_KEYVAULT_URL is set;
// otherwise returns an EnvelopePostgresStore for local use.
func buildSecretStore(pool *pgxpool.Pool, orgID pgtype.UUID, master []byte) (secretstore.SecretStore, error) {
	kvURL := os.Getenv("AZURE_KEYVAULT_URL")
	if kvURL == "" {
		return secretstore.NewEnvelopePostgresStore(pool, orgID, master), nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	kvClient, err := secretstoreazure.NewRealKVClient(kvURL, cred)
	if err != nil {
		return nil, fmt.Errorf("keyvault client: %w", err)
	}
	return secretstoreazure.NewKeyVaultStore(kvClient), nil
}
