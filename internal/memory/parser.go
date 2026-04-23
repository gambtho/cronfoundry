// Package memory extracts the reserved <memory>...</memory> writeback block
// from an LLM's output.
package memory

import (
	"regexp"
	"strings"
)

var blockRe = regexp.MustCompile(`(?s)<memory>\s*(.*?)\s*</memory>`)

var outputBlockRe = regexp.MustCompile(`(?s)<output\s+name="([^"]+)">\s*(.*?)\s*</output>`)

// Extract returns (publishedOutput, memoryContent, found). When multiple
// blocks are present, the last one is chosen. All blocks are stripped from
// the published output.
func Extract(output string) (string, string, bool) {
	matches := blockRe.FindAllStringSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return output, "", false
	}
	last := matches[len(matches)-1]
	content := strings.TrimSpace(output[last[2]:last[3]])
	published := blockRe.ReplaceAllString(output, "")
	return strings.TrimSpace(published), content, true
}

// ExtractOutputBlocks extracts named <output name="...">...</output> blocks.
// Returns a map of name→content and the remaining text with all output blocks stripped.
// If no blocks are present, the original text is returned as remaining with an empty map.
func ExtractOutputBlocks(output string) (map[string]string, string) {
	matches := outputBlockRe.FindAllStringSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return map[string]string{}, output
	}
	blocks := make(map[string]string, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(output[m[2]:m[3]])
		content := strings.TrimSpace(output[m[4]:m[5]])
		blocks[name] = content
	}
	remaining := strings.TrimSpace(outputBlockRe.ReplaceAllString(output, ""))
	return blocks, remaining
}
