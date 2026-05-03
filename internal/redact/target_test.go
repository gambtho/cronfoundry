package redact

import "testing"

func TestTarget(t *testing.T) {
	cases := []struct {
		kind, raw, want string
	}{
		{"slack", "https://hooks.slack.com/services/T0/B0/secret", "hooks.slack.com"},
		{"discord", "https://discord.com/api/webhooks/123/secret", "discord.com"},
		{"teams", "https://outlook.office.com/webhook/xyz", "outlook.office.com"},
		{"github-issue", "https://api.github.com/repos/org/repo/issues", "org/repo"},
		{"slack", "#alerts", "#alerts"},
		{"email", "team@example.com", "team@example.com"},
		{"unknown", "anything", "anything"},
	}
	for _, c := range cases {
		if got := Target(c.kind, c.raw); got != c.want {
			t.Errorf("Target(%q,%q) = %q, want %q", c.kind, c.raw, got, c.want)
		}
	}
}
