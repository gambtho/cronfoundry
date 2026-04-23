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

func TestExtractOutputBlocks_Single(t *testing.T) {
	input := `<output name="summary">Hello world</output> some trailing text`
	blocks, remaining := ExtractOutputBlocks(input)
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	if blocks["summary"] != "Hello world" {
		t.Errorf("want 'Hello world', got %q", blocks["summary"])
	}
	if remaining != "some trailing text" {
		t.Errorf("want 'some trailing text', got %q", remaining)
	}
}

func TestExtractOutputBlocks_Multiple(t *testing.T) {
	input := `<output name="summary">Short</output><output name="full_report">Long report</output>`
	blocks, remaining := ExtractOutputBlocks(input)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if blocks["summary"] != "Short" {
		t.Errorf("summary: got %q", blocks["summary"])
	}
	if blocks["full_report"] != "Long report" {
		t.Errorf("full_report: got %q", blocks["full_report"])
	}
	if remaining != "" {
		t.Errorf("remaining should be empty, got %q", remaining)
	}
}

func TestExtractOutputBlocks_None(t *testing.T) {
	input := "plain output with no blocks"
	blocks, remaining := ExtractOutputBlocks(input)
	if len(blocks) != 0 {
		t.Fatalf("want 0 blocks, got %d", len(blocks))
	}
	if remaining != input {
		t.Errorf("remaining should equal input, got %q", remaining)
	}
}
