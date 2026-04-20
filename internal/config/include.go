package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var includeRe = regexp.MustCompile(`\{\{\s*include\s+"([^"]+)"\s*\}\}`)

// ResolveIncludes replaces `{{ include "path" }}` directives with the file
// contents read from `root`. The directive is single-level only — included
// content is inserted verbatim without re-processing.
//
// Security:
//   - absolute paths are rejected
//   - paths that escape `root` (via `..`) are rejected
func ResolveIncludes(body, root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	var firstErr error
	result := includeRe.ReplaceAllStringFunc(body, func(match string) string {
		if firstErr != nil {
			return match
		}
		sub := includeRe.FindStringSubmatch(match)
		rel := sub[1]
		if filepath.IsAbs(rel) {
			firstErr = fmt.Errorf("include path %q must not be absolute", rel)
			return match
		}
		joined := filepath.Join(absRoot, rel)
		cleaned, err := filepath.Abs(joined)
		if err != nil {
			firstErr = fmt.Errorf("resolve include %q: %w", rel, err)
			return match
		}
		if !strings.HasPrefix(cleaned, absRoot+string(os.PathSeparator)) && cleaned != absRoot {
			firstErr = fmt.Errorf("include %q resolves outside root", rel)
			return match
		}
		data, err := os.ReadFile(cleaned)
		if err != nil {
			firstErr = fmt.Errorf("read include %q: %w", rel, err)
			return match
		}
		return string(data)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}
