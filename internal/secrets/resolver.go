// Package secrets resolves skill-declared secrets from environment variables
// in the form CRONFOUNDRY_SECRET_<UPPER(name)>.
package secrets

import (
	"fmt"
	"strings"
)

const prefix = "CRONFOUNDRY_SECRET_"

type Resolver struct {
	env map[string]string
}

func New(env map[string]string) *Resolver {
	return &Resolver{env: env}
}

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
