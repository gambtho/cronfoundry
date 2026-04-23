package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider_KnownNames(t *testing.T) {
	cases := []string{"openai", "anthropic", "azure-foundry", "openrouter"}
	for _, name := range cases {
		p, err := NewProvider(name)
		require.NoError(t, err, name)
		assert.NotNil(t, p, name)
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	_, err := NewProvider("llama")
	assert.ErrorContains(t, err, "unknown provider")
}
