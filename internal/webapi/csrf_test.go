package webapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCSRFToken_LengthAndAlphabet(t *testing.T) {
	tok, err := NewCSRFToken()
	require.NoError(t, err)
	// 32 bytes -> 43 chars unpadded base64url
	assert.Len(t, tok, 43)
	assert.False(t, strings.ContainsAny(tok, "+/="), "must be base64url unpadded")
}

func TestNewCSRFToken_UniquePerCall(t *testing.T) {
	a, _ := NewCSRFToken()
	b, _ := NewCSRFToken()
	assert.NotEqual(t, a, b)
}
