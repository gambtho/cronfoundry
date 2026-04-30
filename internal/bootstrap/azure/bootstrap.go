// internal/bootstrap/azure/bootstrap.go
package azure

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Bootstrap orchestrates the end-to-end Azure deploy flow.
type Bootstrap struct {
	Runner       Runner
	Inputs       Inputs
	MasterKey    string
	ParamsPath   string  // where to write the Bicep params file
	TemplateFile string  // path to deploy/main.bicep
	Binary       string  // path to the cronfoundry binary (for admin init)
	DryRun       bool    // skip deploy + everything after
	ImageRoot    string  // override ghcr.io for testing; defaults to ghcrRoot
	HealthScheme string  // "https" by default
	HealthHost   string  // test-only: override fqdn used for /healthz polling
	IPDetector   func(ctx context.Context) (string, error) // override for tests; nil → detectPublicIPDefault
	Stdout       io.Writer
}

// Run executes preflight, image probe, params write, deploy, firewall,
// admin init, restart, and health-wait in order. Honors DryRun.
func (b *Bootstrap) Run(ctx context.Context) error {
	if b.Stdout == nil {
		b.Stdout = io.Discard
	}
	if b.HealthScheme == "" {
		b.HealthScheme = "https"
	}
	if b.IPDetector == nil {
		b.IPDetector = detectPublicIPDefault
	}
	root := b.ImageRoot
	if root == "" {
		root = ghcrRoot
	}

	if err := b.Inputs.Validate(); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> preflight")
	if err := Preflight(ctx, b.Runner); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> probing image")
	if err := probeImageAt(ctx, root, b.Inputs.ImageOwner, b.Inputs.ImageTag); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> writing params:", b.ParamsPath)
	if err := WriteParams(b.Inputs, b.MasterKey, b.ParamsPath); err != nil {
		return err
	}
	if b.DryRun {
		fmt.Fprintln(b.Stdout, "dry-run: skipping deploy")
		return nil
	}
	fmt.Fprintln(b.Stdout, "==> deploying (this takes ~10 minutes)")
	if err := Deploy(ctx, b.Runner, b.Inputs.Region, b.TemplateFile, b.ParamsPath); err != nil {
		return err
	}
	ip, err := b.IPDetector(ctx)
	if err != nil {
		return fmt.Errorf("detect public ip: %w", err)
	}
	fmt.Fprintln(b.Stdout, "==> opening postgres firewall to", ip)
	if err := AllowOperatorIP(ctx, b.Runner, b.Inputs.Env, ip); err != nil {
		return err
	}
	dsn := fmt.Sprintf(
		"postgres://cfadmin:%s@cf-pg-%s.postgres.database.azure.com:5432/cronfoundry?sslmode=require",
		b.Inputs.PostgresPassword, b.Inputs.Env)
	fmt.Fprintln(b.Stdout, "==> running admin init")
	if err := AdminInit(ctx, b.Runner, b.Binary, dsn, b.MasterKey, "default"); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> restarting serve revision")
	if err := RestartServe(ctx, b.Runner, b.Inputs.Env); err != nil {
		return err
	}
	out, err := b.Runner.Run(ctx,
		"az", "containerapp", "show",
		"--resource-group", "rg-cronfoundry-"+b.Inputs.Env,
		"--name", "cf-serve-"+b.Inputs.Env,
		"--query", "properties.configuration.ingress.fqdn",
		"-o", "tsv",
	)
	if err != nil {
		return fmt.Errorf("discover fqdn: %w", err)
	}
	fqdn := strings.TrimSpace(string(out))
	healthHost := fqdn
	if b.HealthHost != "" {
		healthHost = b.HealthHost
	}
	fmt.Fprintln(b.Stdout, "==> waiting for /healthz at", healthHost)
	if err := waitHealthyAt(ctx, b.HealthScheme, healthHost, 5*time.Minute, 5*time.Second); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout)
	fmt.Fprintln(b.Stdout, "Deploy complete.")
	fmt.Fprintln(b.Stdout, "  Login URL:        https://"+fqdn+"/")
	fmt.Fprintln(b.Stdout, "  GitHub App URLs:  paste https://"+fqdn+" into Homepage / Callback / Webhook")
	return nil
}

func detectPublicIPDefault(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cronfoundry-bootstrap")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
