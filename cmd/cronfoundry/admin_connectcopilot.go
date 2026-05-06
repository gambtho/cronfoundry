package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secrets/server"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// connectCopilotDeps captures the injectable dependencies for the
// connect-copilot command so tests can stub the GitHub endpoints and the
// secret-store side effect.
type connectCopilotDeps struct {
	DeviceCodeURL string
	TokenURL      string
	ClientID      string
	Scope         string
	PollInterval  time.Duration
	HTTPClient    *http.Client
	StoreTokens   func(ctx context.Context, prefix, accessToken, refreshToken string, expiresIn int) error
}

func (d *connectCopilotDeps) applyDefaults() {
	if d.DeviceCodeURL == "" {
		d.DeviceCodeURL = "https://github.com/login/device/code"
	}
	if d.TokenURL == "" {
		d.TokenURL = "https://github.com/login/oauth/access_token"
	}
	if d.ClientID == "" {
		d.ClientID = webapi.CopilotClientID
	}
	if d.Scope == "" {
		d.Scope = "copilot"
	}
	// PollInterval is intentionally not defaulted: a zero value means
	// "poll as fast as ctx allows" (used by tests). Production callers
	// set an explicit cadence via prodConnectCopilotDeps.
	if d.HTTPClient == nil {
		d.HTTPClient = http.DefaultClient
	}
}

type deviceCodeResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

func newAdminConnectCopilotCmd(deps connectCopilotDeps) *cobra.Command {
	deps.applyDefaults()
	var prefix string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "connect-copilot",
		Short: "Run the GitHub device-flow OAuth for Copilot Enterprise and store tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminConnectCopilot(cmd.Context(), deps, prefix, timeout, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "copilot", "secret-name prefix to store tokens under")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "overall timeout for the device flow")
	return cmd
}

func runAdminConnectCopilot(ctx context.Context, deps connectCopilotDeps, prefix string, timeout time.Duration, out io.Writer) error {
	if prefix == "" {
		return fmt.Errorf("--prefix must not be empty")
	}
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	if deps.StoreTokens == nil {
		return errors.New("StoreTokens not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dc, err := requestDeviceCode(ctx, deps)
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}

	if _, err := fmt.Fprintf(out, "Visit %s and enter code %s\n", dc.VerificationURI, dc.UserCode); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	tok, err := pollForToken(ctx, deps, dc)
	if err != nil {
		return fmt.Errorf("poll for token: %w", err)
	}

	if err := deps.StoreTokens(ctx, prefix, tok.AccessToken, tok.RefreshToken, tok.ExpiresIn); err != nil {
		return fmt.Errorf("store tokens: %w", err)
	}
	if _, err := fmt.Fprintf(out, "Stored tokens with prefix %q\n", prefix); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func requestDeviceCode(ctx context.Context, deps connectCopilotDeps) (*deviceCodeResp, error) {
	body := url.Values{
		"client_id": {deps.ClientID},
		"scope":     {deps.Scope},
	}

	// Transient TLS handshake / connection-reset errors against
	// github.com/login/device/code surface here; without a retry, the entire
	// quickstart aborts at step 22 and the operator has to re-run the whole
	// script. Retry up to 3 times with exponential backoff.
	const maxAttempts = 3
	var resp *http.Response
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, deps.DeviceCodeURL,
			strings.NewReader(body.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, lastErr = deps.HTTPClient.Do(req)
		if lastErr == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, lastErr
		}
		if attempt < maxAttempts {
			delay := time.Duration(attempt) * 2 * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("device-code endpoint returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dc deviceCodeResp
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("decode device-code response: %w", err)
	}
	if dc.DeviceCode == "" {
		return nil, errors.New("device-code response missing device_code")
	}
	return &dc, nil
}

func pollForToken(ctx context.Context, deps connectCopilotDeps, dc *deviceCodeResp) (*tokenResp, error) {
	interval := deps.PollInterval
	if dc.Interval > 0 && deps.PollInterval > 0 {
		interval = time.Duration(dc.Interval) * time.Second
	}
	for {
		params := url.Values{
			"client_id":   {deps.ClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, deps.TokenURL,
			strings.NewReader(params.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := deps.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		var tr tokenResp
		decErr := json.NewDecoder(resp.Body).Decode(&tr)
		_ = resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decode token response: %w", decErr)
		}

		switch tr.Error {
		case "":
			if tr.AccessToken == "" {
				return nil, errors.New("token response missing access_token")
			}
			return &tr, nil
		case "authorization_pending":
			// keep polling at the current cadence
		case "slow_down":
			// GitHub asks us to back off; bump the interval before sleeping.
			if interval <= 0 {
				interval = 5 * time.Second
			}
			interval += 5 * time.Second
		default:
			return nil, fmt.Errorf("token endpoint error: %s", tr.Error)
		}

		if interval <= 0 {
			// Tight loop for tests; still respect ctx.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// prodConnectCopilotDeps wires the production StoreTokens closure that opens a
// pgx pool, locates the seeded organization, and writes the three secrets
// expected by the webapi token-refresh path.
func prodConnectCopilotDeps() connectCopilotDeps {
	return connectCopilotDeps{
		PollInterval: 5 * time.Second,
		StoreTokens:  storeCopilotTokensProd,
	}
}

func storeCopilotTokensProd(ctx context.Context, prefix, accessToken, refreshToken string, expiresIn int) error {
	useKV := os.Getenv("AZURE_KEYVAULT_URL") != ""

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var (
		store server.SecretStore
		err   error
	)
	if useKV {
		// KV path doesn't need DB pool / org / master key — buildSecretStore
		// returns the KeyVaultStore immediately when AZURE_KEYVAULT_URL is set.
		store, err = buildSecretStore(nil, pgtype.UUID{}, nil)
		if err != nil {
			return fmt.Errorf("build secret store: %w", err)
		}
	} else {
		dsn := os.Getenv(envDatabaseURL)
		if dsn == "" {
			return fmt.Errorf("%s is required", envDatabaseURL)
		}
		encodedMaster := os.Getenv(envMasterKey)
		if encodedMaster == "" {
			return fmt.Errorf("%s is required; run `cronfoundry admin init` first", envMasterKey)
		}
		master, err := server.ParseMasterKey(encodedMaster)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envMasterKey, err)
		}

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

		store, err = buildSecretStore(pool, org.ID, master)
		if err != nil {
			return fmt.Errorf("build secret store: %w", err)
		}
	}

	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
	if err := store.Put(ctx, prefix+"-access-token", accessToken); err != nil {
		return fmt.Errorf("put access token: %w", err)
	}
	if err := store.Put(ctx, prefix+"-refresh-token", refreshToken); err != nil {
		return fmt.Errorf("put refresh token: %w", err)
	}
	if err := store.Put(ctx, prefix+"-expiry", strconv.FormatInt(expiresAt.Unix(), 10)); err != nil {
		return fmt.Errorf("put expiry: %w", err)
	}
	return nil
}
