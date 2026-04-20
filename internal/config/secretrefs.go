package config

import (
	"encoding/json"
	"sort"
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

// scanSecretRefs finds every `{"secret": "<name>"}` occurrence in the JSON
// tree and adds the name to seen. Non-string values are skipped.
func scanSecretRefs(raw json.RawMessage, seen map[string]struct{}) {
	if len(raw) == 0 {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return
	}
	walkSecretRefs(v, seen)
}

func walkSecretRefs(v any, seen map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		if s, ok := t["secret"].(string); ok && s != "" {
			seen[s] = struct{}{}
		}
		for _, child := range t {
			walkSecretRefs(child, seen)
		}
	case []any:
		for _, child := range t {
			walkSecretRefs(child, seen)
		}
	}
}
