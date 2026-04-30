// internal/bootstrap/azure/admininit.go
package azure

import (
	"context"
	"fmt"
)

// AdminInit shells out to `<binary> admin init --org-name <org>` with
// CRONFOUNDRY_DATABASE_URL and CRONFOUNDRY_MASTER_KEY set in the env.
func AdminInit(ctx context.Context, r Runner, binary, dsn, masterKey, orgName string) error {
	env := []string{
		"CRONFOUNDRY_DATABASE_URL=" + dsn,
		"CRONFOUNDRY_MASTER_KEY=" + masterKey,
	}
	if err := r.RunWithEnv(ctx, env, binary, "admin", "init", "--org-name", orgName); err != nil {
		return fmt.Errorf("admin init: %w", err)
	}
	return nil
}
