// internal/bootstrap/azure/preflight.go
package azure

import (
	"context"
	"fmt"
)

// Preflight verifies az is logged in and bicep is installed. Returns a
// typed error with a remediation hint.
func Preflight(ctx context.Context, r Runner) error {
	if _, err := r.Run(ctx, "az", "account", "show"); err != nil {
		return fmt.Errorf("az login required: %w (run `az login` then retry)", err)
	}
	if _, err := r.Run(ctx, "az", "bicep", "version"); err != nil {
		return fmt.Errorf("bicep CLI required: %w (run `az bicep install` then retry)", err)
	}
	return nil
}
