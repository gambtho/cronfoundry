// Package runner resolves skill-declared secrets from environment variables
// in the form CRONFOUNDRY_SECRET_<UPPER(name)>.
package runner

import (
	"fmt"
	"strings"
)

const prefix = "CRONFOUNDRY_SECRET_"

// Resolver looks up skill secrets from an environment snapshot.
type Resolver struct {
	env map[string]string
}

// New returns a Resolver backed by the given environment map, typically the
// process environment parsed into key/value form.
func New(env map[string]string) *Resolver {
	return &Resolver{env: env}
}

// Get returns the value of the named secret, or an error naming the missing
// environment variable the caller must export.
func (r *Resolver) Get(name string) (string, error) {
	key := prefix + strings.ToUpper(name)
	v, ok := r.env[key]
	if !ok {
		return "", fmt.Errorf("secret %q not set; export %s", name, key)
	}
	return v, nil
}

// AllValues returns every secret value known to the resolver, for use by
// the redactor. Order is not guaranteed.
func (r *Resolver) AllValues() []string {
	out := make([]string, 0)
	for k, v := range r.env {
		if strings.HasPrefix(k, prefix) && v != "" {
			out = append(out, v)
		}
	}
	return out
}
