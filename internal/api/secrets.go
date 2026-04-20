package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ServeHTTP implements GET /internal/secrets?names=a,b,c.
//
// Each requested name must appear in the JWT's SecretRefs claim; otherwise
// the handler returns 403 without revealing whether the secret exists. This
// is the cryptographic enforcement of per-run secret scoping.
func (h secretsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	allowed := make(map[string]struct{}, len(claims.SecretRefs))
	for _, n := range claims.SecretRefs {
		allowed[n] = struct{}{}
	}

	namesParam := r.URL.Query().Get("names")
	if namesParam == "" {
		http.Error(w, "missing names query parameter", http.StatusBadRequest)
		return
	}
	names := strings.Split(namesParam, ",")

	out := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			http.Error(w, "secret not in token scope: "+name, http.StatusForbidden)
			return
		}
		val, err := h.deps.Secrets.Get(r.Context(), name)
		if err != nil {
			http.Error(w, "load secret: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out[name] = val
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
