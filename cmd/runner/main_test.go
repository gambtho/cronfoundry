package main

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/gambtho/cronfoundry/internal/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactingWriter_ScrubsSecretsOnStream(t *testing.T) {
	var buf bytes.Buffer
	r := redact.New([]string{"sk-abc-123"})
	w := &redactingWriter{inner: &buf, r: r}

	n, err := w.Write([]byte("Token=sk-abc-123 extra"))
	require.NoError(t, err)
	assert.Equal(t, len("Token=sk-abc-123 extra"), n)
	assert.Equal(t, "Token=[REDACTED] extra", buf.String())
}

func TestRedactingHandler_ScrubsNonStringAttrs(t *testing.T) {
	var buf bytes.Buffer
	r := redact.New([]string{"sk-abc-123"})
	inner := slog.NewTextHandler(&buf, nil)
	h := redactingHandler{inner: inner, r: r}
	logger := slog.New(h)

	// Pass an error whose message contains the secret. The err attr is
	// KindAny, so the old handler would have skipped redaction entirely.
	logger.Error("run failed", "err", errors.New("bad key: sk-abc-123"))

	out := buf.String()
	assert.Contains(t, out, "[REDACTED]")
	assert.NotContains(t, out, "sk-abc-123", "secret leaked through non-string attr")
}
