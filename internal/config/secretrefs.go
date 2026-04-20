package config

import (
	"encoding/json"
	"sort"
	"strings"
)

// CollectSecretRefs extracts all secret names referenced from destinations
// JSON, env JSON, and an optional LLM secret reference. Secret references
// appear in the form `{ "secret": "name" }` anywhere in the destinations
// or env JSON trees.
//
// Results are sorted and deduplicated.
func CollectSecretRefs(destinations, env json.RawMessage, llmRef *string) []string {
	seen := map[string]struct{}{}
	scanSecretRefs(destinations, seen)
	scanSecretRefs(env, seen)
	if llmRef != nil && *llmRef != "" {
		seen[*llmRef] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// scanSecretRefs finds every `"secret" : "<name>"` pair in raw JSON and adds
// the name to seen. Non-string values are skipped without aborting.
func scanSecretRefs(raw json.RawMessage, seen map[string]struct{}) {
	if len(raw) == 0 {
		return
	}
	s := string(raw)
	i := 0
	for {
		idx := strings.Index(s[i:], `"secret"`)
		if idx < 0 {
			return
		}
		idx += i
		j := idx + len(`"secret"`)
		// Skip whitespace + colon.
		for j < len(s) && (s[j] == ' ' || s[j] == ':' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
			j++
		}
		if j >= len(s) || s[j] != '"' {
			// Not a string value — skip this occurrence and keep scanning.
			i = idx + len(`"secret"`)
			continue
		}
		j++ // past opening quote
		end := strings.IndexByte(s[j:], '"')
		if end < 0 {
			return
		}
		end += j
		name := s[j:end]
		if name != "" {
			seen[name] = struct{}{}
		}
		i = end + 1
	}
}
