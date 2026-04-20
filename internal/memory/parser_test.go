package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtract_Present(t *testing.T) {
	output := `Here is the digest.

Lots of stuff happened.

<memory>
Learned: team dislikes lorem ipsum.
Date: 2026-04-13
</memory>
`
	published, mem, ok := Extract(output)
	require.True(t, ok)
	assert.Equal(t, "Learned: team dislikes lorem ipsum.\nDate: 2026-04-13", mem)
	assert.NotContains(t, published, "<memory>")
	assert.NotContains(t, published, "Learned: team dislikes")
	assert.Contains(t, published, "Lots of stuff happened.")
}

func TestExtract_Absent(t *testing.T) {
	output := "Plain output, no memory block."
	published, mem, ok := Extract(output)
	assert.False(t, ok)
	assert.Equal(t, "", mem)
	assert.Equal(t, output, published)
}

func TestExtract_MultipleBlocks_UsesLast(t *testing.T) {
	output := `First attempt: <memory>old</memory>
Revised: <memory>new</memory>`
	published, mem, ok := Extract(output)
	require.True(t, ok)
	assert.Equal(t, "new", mem)
	assert.NotContains(t, published, "<memory>new")
}
