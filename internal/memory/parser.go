// Package memory extracts the reserved <memory>...</memory> writeback block
// from an LLM's output.
package memory

import (
	"regexp"
	"strings"
)

var blockRe = regexp.MustCompile(`(?s)<memory>\s*(.*?)\s*</memory>`)

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
