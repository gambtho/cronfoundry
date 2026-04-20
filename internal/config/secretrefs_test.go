package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectSecretRefs_ExtractsFromDestinations(t *testing.T) {
	dests := json.RawMessage(`[{"slack":{"secret":"slack_url"}},{"discord":{"secret":"discord_url"}}]`)
	got := CollectSecretRefs(dests, nil, nil)
	assert.ElementsMatch(t, []string{"slack_url", "discord_url"}, got)
}

func TestCollectSecretRefs_ExtractsFromEnv(t *testing.T) {
	env := json.RawMessage(`{"API_KEY":{"secret":"api_key"},"PLAIN":"value"}`)
	got := CollectSecretRefs(nil, env, nil)
	assert.Equal(t, []string{"api_key"}, got)
}

func TestCollectSecretRefs_IncludesLLMRef(t *testing.T) {
	llm := "openai_key"
	got := CollectSecretRefs(nil, nil, &llm)
	assert.Equal(t, []string{"openai_key"}, got)
}

func TestCollectSecretRefs_Dedupes(t *testing.T) {
	dests := json.RawMessage(`[{"slack":{"secret":"shared"}}]`)
	env := json.RawMessage(`{"X":{"secret":"shared"}}`)
	llm := "shared"
	got := CollectSecretRefs(dests, env, &llm)
	assert.Equal(t, []string{"shared"}, got)
}

func TestCollectSecretRefs_SkipsNonStringSecretValues(t *testing.T) {
	dests := json.RawMessage(`[{"x":{"secret":42}},{"y":{"secret":"real"}}]`)
	got := CollectSecretRefs(dests, nil, nil)
	assert.Equal(t, []string{"real"}, got)
}

func TestCollectSecretRefs_EmptyInputs(t *testing.T) {
	assert.Empty(t, CollectSecretRefs(nil, nil, nil))
	assert.Empty(t, CollectSecretRefs(json.RawMessage(``), json.RawMessage(``), nil))
}
