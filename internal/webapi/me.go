package webapi

import (
	"encoding/json"
	"net/http"
)

type meHandler struct{ masterKey []byte }

func (h meHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims := SessionClaimsFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"login": claims.Login,
		"role":  claims.Role,
	})
}
