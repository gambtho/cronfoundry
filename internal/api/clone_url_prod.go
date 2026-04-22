//go:build !e2e

package api

// smokeCloneOverride is a compile-time no-op in production builds.
// See clone_url_e2e.go for the test-only implementation.
func smokeCloneOverride() (string, bool) { return "", false }
