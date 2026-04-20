package publish

// Test-only helpers shared across the webhook publishers' tests. The _test.go
// suffix keeps them out of the non-test build while preserving package-internal
// visibility for slack_test.go, discord_test.go, and teams_test.go.

type mapSecrets map[string]string

func (m mapSecrets) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", &missingSecret{key: k}
	}
	return v, nil
}

type missingSecret struct{ key string }

func (m *missingSecret) Error() string { return "missing secret: " + m.key }
