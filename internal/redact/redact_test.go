package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactor_ReplacesKnownValues(t *testing.T) {
	r := New([]string{"sk-abc-123", "hooks.slack.com/TOKEN123", ""})
	got := r.Redact("Key=sk-abc-123 url=https://hooks.slack.com/TOKEN123/extra")
	assert.Equal(t, "Key=[REDACTED] url=https://[REDACTED]/extra", got)
}

func TestRedactor_EmptyStringsIgnored(t *testing.T) {
	r := New([]string{""})
	got := r.Redact("no secrets here")
	assert.Equal(t, "no secrets here", got)
}

func TestRedactor_LongestMatchFirst(t *testing.T) {
	r := New([]string{"secret", "supersecret"})
	got := r.Redact("value=supersecret")
	assert.Equal(t, "value=[REDACTED]", got)
}
