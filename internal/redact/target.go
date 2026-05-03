package redact

import (
	"net/url"
	"regexp"
	"strings"
)

// Target returns a sanitized, human-readable identifier for a publish
// destination, suitable for storage and display. Webhook URLs collapse
// to their host. github-issue URLs collapse to "<owner>/<repo>".
// Channel-style or email targets pass through. Inputs that can't be
// classified pass through unchanged.
func Target(kind, raw string) string {
	raw = strings.TrimSpace(raw)
	switch kind {
	case "slack", "discord", "teams", "http-json", "http":
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			if u, err := url.Parse(raw); err == nil && u.Host != "" {
				return u.Host
			}
		}
		return raw
	case "github-issue":
		if m := ghRepoRE.FindStringSubmatch(raw); m != nil {
			return m[1] + "/" + m[2]
		}
		return raw
	default:
		return raw
	}
}

var ghRepoRE = regexp.MustCompile(`github\.com/(?:repos/)?([^/]+)/([^/]+)`)
