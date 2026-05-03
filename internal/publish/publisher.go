// Package publish fans output to destinations (GitHub issue, Slack, Discord,
// Teams), isolating per-destination failures.
package publish

import (
	"context"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/template"
)

// Result is the outcome of a single publish attempt.
type Result struct {
	Type       string // "github-issue" | "slack" | "discord" | "teams"
	OK         bool
	Skipped    bool   // true when the destination's when-condition was not met
	SkipReason string // human-readable explanation when Skipped==true
	Err        error  // non-nil when OK == false
	Detail     string // optional context (issue URL, HTTP status, etc.)
	// Target is the raw destination identifier the publisher was asked to
	// deliver to (channel, webhook URL, repo path, email address). It may
	// contain secret material — callers MUST redact via redact.Target
	// before persisting or logging.
	Target string
}

// Publisher publishes a rendered output to a single destination.
// Implementations resolve their own secrets (via SecretGetter) and handle
// their own retries.
type Publisher interface {
	Type() string
	Publish(ctx context.Context, dest config.Destination, output string, tctx template.Context, secrets SecretGetter) Result
}

// SecretGetter retrieves a secret value by logical name.
type SecretGetter interface {
	Get(name string) (string, error)
}
