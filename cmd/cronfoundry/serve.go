package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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
	"github.com/gambtho/cronfoundry/internal/jobdispatch/flymachines"
	"github.com/gambtho/cronfoundry/internal/jobdispatch/k8sjobs"
	"github.com/gambtho/cronfoundry/internal/metrics"
	"github.com/gambtho/cronfoundry/internal/scheduler"
	"github.com/gambtho/cronfoundry/internal/secrets/server"
	"github.com/gambtho/cronfoundry/internal/secrets/server/azurekv"
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
	envTrustProxy        = "CRONFOUNDRY_TRUST_PROXY"
	envRateAPIRPM        = "CRONFOUNDRY_RATE_API_RPM"
	envRateOAuthRPM      = "CRONFOUNDRY_RATE_OAUTH_RPM"
	envRateWebhookRPM    = "CRONFOUNDRY_RATE_WEBHOOK_RPM"
	envRateSSEConcurrent = "CRONFOUNDRY_RATE_SSE_MAX_CONCURRENT"
	envRateLRUSize       = "CRONFOUNDRY_RATE_LRU_SIZE"
	envRateDisabled      = "CRONFOUNDRY_RATE_DISABLED"
	envPublicBaseURL     = "CRONFOUNDRY_PUBLIC_BASE_URL"
	envMetricsDisabled   = "CRONFOUNDRY_METRICS_DISABLED"
	envChatProvider      = "CRONFOUNDRY_CHAT_PROVIDER"
	envChatModel         = "CRONFOUNDRY_CHAT_MODEL"
	envChatAPIKeySecret  = "CRONFOUNDRY_CHAT_API_KEY_SECRET"
	envChatMaxTurns      = "CRONFOUNDRY_CHAT_MAX_TURNS"
	envChatMaxTokens     = "CRONFOUNDRY_CHAT_MAX_TOKENS"
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
	master, err := server.ParseMasterKey(masterEnc)
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
		RunnerAPIKey:  os.Getenv("CRONFOUNDRY_RUNNER_API_KEY"),
	})
	rateCfg := webapi.RateLimiterConfig{
		TrustProxy:       envBool(envTrustProxy),
		Disabled:         envBool(envRateDisabled),
		APIRPM:           envInt(envRateAPIRPM, 60),
		OAuthRPM:         envInt(envRateOAuthRPM, 10),
		WebhookRPM:       envInt(envRateWebhookRPM, 300),
		SSEMaxConcurrent: envInt(envRateSSEConcurrent, 5),
		LRUSize:          envInt(envRateLRUSize, 4096),
	}
	if os.Getenv(envPublicBaseURL) == "" {
		slog.Warn("CRONFOUNDRY_PUBLIC_BASE_URL not set; CSRF Origin check disabled (dev mode)")
	}
	metrics.Disabled = envBool(envMetricsDisabled)

	// Chat is opt-in. The operator enables it by setting the three core
	// env vars (provider/model/secret name). When any one is missing we
	// keep the feature disabled but the rest of the service still comes
	// up — chat is a help surface, not a critical path.
	chatCfg := webapi.ChatConfig{
		Provider:     os.Getenv(envChatProvider),
		Model:        os.Getenv(envChatModel),
		APIKeySecret: os.Getenv(envChatAPIKeySecret),
		MaxTurns:     envInt(envChatMaxTurns, 0),
		MaxTokens:    envInt(envChatMaxTokens, 0),
	}
	if chatCfg.Provider != "" && chatCfg.Model != "" && chatCfg.APIKeySecret != "" {
		chatCfg.Enabled = true
		slog.Info("serve: chat assistant enabled", "provider", chatCfg.Provider, "model", chatCfg.Model)
	} else {
		slog.Info("serve: chat assistant disabled (set CRONFOUNDRY_CHAT_PROVIDER/_MODEL/_API_KEY_SECRET to enable)")
	}

	clock := &scheduler.TickClock{}
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
		PublicBaseURL:     os.Getenv(envPublicBaseURL),
		Syncer:            poller,
		RateLimit:         rateCfg,
		Clock:             clock,
		SweepInterval:     cadence,
		Chat:              chatCfg,
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
		Pool:          pool,
		Signer:        signer,
		Dispatcher:    dispatcher,
		Installations: installs,
		APIBaseURL:    "http://" + addr,
		RunnerAPIURL:  runnerAPIURL,
		RunnerBinary:  self,
		Clock:         clock,
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

// buildJobDispatcher returns the appropriate JobDispatcher based on env vars.
// Priority: Fly.io > Kubernetes > Azure Container Apps > Subprocess (local).
func buildJobDispatcher() (cloud.JobDispatcher, error) {
	// Fly.io — checked first; FLY_RUNNER_APP is unambiguous
	if app := os.Getenv("FLY_RUNNER_APP"); app != "" {
		token := os.Getenv("FLY_API_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("FLY_RUNNER_APP is set but FLY_API_TOKEN is missing")
		}
		image := os.Getenv("FLY_RUNNER_IMAGE")
		if image == "" {
			return nil, fmt.Errorf("FLY_RUNNER_APP is set but FLY_RUNNER_IMAGE is missing")
		}
		client := flymachines.NewRealFlyClient(token)
		return flymachines.NewDispatcher(client, flymachines.Config{App: app, Image: image}), nil
	}

	// AKS / Kubernetes
	if ns := os.Getenv("K8S_RUNNER_NAMESPACE"); ns != "" {
		image := os.Getenv("K8S_RUNNER_IMAGE")
		if image == "" {
			return nil, fmt.Errorf("K8S_RUNNER_NAMESPACE is set but K8S_RUNNER_IMAGE is missing")
		}
		sa := os.Getenv("K8S_RUNNER_SERVICE_ACCOUNT")
		if sa == "" {
			sa = "cf-runner"
		}
		k8sClient, err := k8sjobs.NewInClusterClient()
		if err != nil {
			return nil, fmt.Errorf("k8s in-cluster client: %w", err)
		}
		return k8sjobs.NewDispatcher(k8sClient, k8sjobs.Config{
			Namespace:      ns,
			RunnerImage:    image,
			ServiceAccount: sa,
		}), nil
	}

	// Azure Container Apps Jobs
	rg := os.Getenv("AZURE_CAE_RESOURCE_GROUP")
	jobName := os.Getenv("AZURE_CAE_JOB_NAME")
	subID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	image := os.Getenv("AZURE_CAE_JOB_IMAGE")
	if rg == "" || jobName == "" || subID == "" {
		if rg != "" || jobName != "" || subID != "" {
			return nil, fmt.Errorf("partial Azure dispatcher config: AZURE_CAE_RESOURCE_GROUP=%v AZURE_CAE_JOB_NAME=%v AZURE_SUBSCRIPTION_ID=%v — all three are required",
				rg != "", jobName != "", subID != "")
		}
		return cloud.NewSubprocessDispatcher(), nil
	}
	if image == "" {
		return nil, fmt.Errorf("AZURE_CAE_JOB_IMAGE is required when Azure dispatcher is configured")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	armClient, err := cloudazure.NewRealARMJobsClient(subID, cred)
	if err != nil {
		return nil, fmt.Errorf("arm jobs client: %w", err)
	}
	return cloudazure.NewContainerAppsJobDispatcher(cloudazure.DispatcherConfig{
		Client:        armClient,
		ResourceGroup: rg,
		JobName:       jobName,
		Image:         image,
	}), nil
}

// buildSecretStore returns a KeyVaultStore when AZURE_KEYVAULT_URL is set;
// otherwise returns an EnvelopePostgresStore for local use.
func buildSecretStore(pool *pgxpool.Pool, orgID pgtype.UUID, master []byte) (server.SecretStore, error) {
	kvURL := os.Getenv("AZURE_KEYVAULT_URL")
	if kvURL == "" {
		return server.NewEnvelopePostgresStore(pool, orgID, master), nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	kvClient, err := azurekv.NewRealKVClient(kvURL, cred)
	if err != nil {
		return nil, fmt.Errorf("keyvault client: %w", err)
	}
	return azurekv.NewKeyVaultStore(kvClient), nil
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}
