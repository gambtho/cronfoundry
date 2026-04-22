//go:build e2e

package api

import "os"

// smokeCloneOverride returns the CRONFOUNDRY_SMOKE_CLONE_URL env value
// when set. This helper exists ONLY in e2e-tagged builds; production
// binaries replace it with a no-op at compile time (see clone_url_prod.go).
func smokeCloneOverride() (string, bool) {
	v := os.Getenv("CRONFOUNDRY_SMOKE_CLONE_URL")
	return v, v != ""
}
