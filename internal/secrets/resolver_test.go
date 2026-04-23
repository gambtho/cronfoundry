package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolver_GetSecret(t *testing.T) {
	r := New(map[string]string{
		"CRONFOUNDRY_SECRET_SLACK_DIGEST_WEBHOOK": "https://hooks.slack.com/xxx",
		"CRONFOUNDRY_SECRET_TEAM_NAME":            "Platform",
	})

	v, err := r.Get("slack_digest_webhook")
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/xxx", v)

	v, err = r.Get("team_name")
	require.NoError(t, err)
	assert.Equal(t, "Platform", v)

	_, err = r.Get("missing")
	assert.ErrorContains(t, err, "secret \"missing\"")
	assert.ErrorContains(t, err, "CRONFOUNDRY_SECRET_MISSING")
}

func TestResolver_AllValues_ForRedaction(t *testing.T) {
	r := New(map[string]string{
		"CRONFOUNDRY_SECRET_A": "secret-a",
		"CRONFOUNDRY_SECRET_B": "secret-b",
		"OTHER":                "not-a-secret",
	})
	values := r.AllValues()
	assert.ElementsMatch(t, []string{"secret-a", "secret-b"}, values)
}

func TestResolver_AllValues_IndependentOfGet(t *testing.T) {
	// AllValues must return all secret values even if Get was never called.
	// The redactor calls AllValues() before any secret resolution happens.
	r := New(map[string]string{
		"CRONFOUNDRY_SECRET_TOKEN": "tok_abc",
		"CRONFOUNDRY_SECRET_KEY":   "key_xyz",
		"UNRELATED":                "not-a-secret",
	})

	// Do NOT call r.Get() first — verify AllValues() is unconditional.
	vals := r.AllValues()
	assert.ElementsMatch(t, []string{"tok_abc", "key_xyz"}, vals)
}
