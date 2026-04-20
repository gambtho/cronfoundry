// Package redact scrubs known secret values from strings before logging.
package redact

import (
	"sort"
	"strings"
)

const placeholder = "[REDACTED]"

type Redactor struct {
	values []string
}

func New(secrets []string) *Redactor {
	filtered := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return len(filtered[i]) > len(filtered[j])
	})
	return &Redactor{values: filtered}
}

func (r *Redactor) Redact(s string) string {
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, placeholder)
	}
	return s
}
