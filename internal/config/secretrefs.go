package config

import (
	"encoding/json"
	"sort"
)

// CollectSecretRefs extracts all secret names referenced from destinations
// JSON, env JSON, and an optional LLM secret reference. Secret references
// appear in the form `{ "secret": "name" }` anywhere in the JSON.
//
// Non-string "secret" values are skipped. Results are sorted + deduplicated.
func CollectSecretRefs(destinations, env json.RawMessage, llmRef *string, extra ...json.RawMessage) []string {
	seen := map[string]struct{}{}
	scan(destinations, seen)
	scan(env, seen)
	for _, e := range extra {
		scan(e, seen)
	}
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

// scan finds every `{"secret":"<name>"}` occurrence in the JSON tree and adds
// the name to seen. Uses a proper JSON walk to avoid false positives.
func scan(raw json.RawMessage, seen map[string]struct{}) {
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
