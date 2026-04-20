package webapi

import (
	"context"
	"net/http"
)

type ctxKey int

const sessionKey ctxKey = 0

// RequireSession is middleware that validates the cf_session cookie.
// Attaches SessionClaims to the request context on success; returns 401 otherwise.
func RequireSession(masterKey []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("cf_session")
		if err != nil {
			http.Error(w, "missing session", http.StatusUnauthorized)
			return
		}
		claims, err := VerifySession(c.Value, masterKey)
		if err != nil {
			http.Error(w, "invalid session", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SessionClaimsFromContext returns the claims attached by RequireSession.
// Returns zero value if the middleware was not applied.
func SessionClaimsFromContext(ctx context.Context) SessionClaims {
	c, _ := ctx.Value(sessionKey).(SessionClaims)
	return c
}
