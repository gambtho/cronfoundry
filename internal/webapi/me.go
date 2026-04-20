package webapi

import (
	"encoding/json"
	"net/http"
)

type meHandler struct{}

func (h meHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims := SessionClaimsFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(map[string]string{
		"login": claims.Login,
		"role":  claims.Role,
	})
	_, _ = w.Write(b)
}
