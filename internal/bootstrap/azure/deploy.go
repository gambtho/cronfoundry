// internal/bootstrap/azure/deploy.go
package azure

import (
	"context"
	"fmt"
	"time"
)

// Deploy invokes `az deployment sub create` and streams output through Runner.
func Deploy(ctx context.Context, r Runner, region, templateFile, paramsFile string) error {
	return r.RunStreaming(ctx,
		"az", "deployment", "sub", "create",
		"--location", region,
		"--template-file", templateFile,
		"--parameters", "@"+paramsFile,
	)
}

// AllowOperatorIP creates a Postgres firewall rule for the operator's IP.
// The rule name embeds the date so repeated runs don't collide.
func AllowOperatorIP(ctx context.Context, r Runner, env, ip string) error {
	rule := "cf-bootstrap-" + time.Now().UTC().Format("20060102")
	_, err := r.Run(ctx,
		"az", "postgres", "flexible-server", "firewall-rule", "create",
		"--resource-group", fmt.Sprintf("rg-cronfoundry-%s", env),
		"--name", fmt.Sprintf("cf-pg-%s", env),
		"--rule-name", rule,
		"--start-ip-address", ip,
		"--end-ip-address", ip,
	)
	return err
}

// RestartServe forces a new revision so the migrated schema is picked up.
// (Failed revisions don't auto-heal after admin init.)
func RestartServe(ctx context.Context, r Runner, env string) error {
	_, err := r.Run(ctx,
		"az", "containerapp", "update",
		"--resource-group", fmt.Sprintf("rg-cronfoundry-%s", env),
		"--name", fmt.Sprintf("cf-serve-%s", env),
		"--set-env-vars", fmt.Sprintf("RESTART_TRIGGER=%d", time.Now().Unix()),
	)
	return err
}
