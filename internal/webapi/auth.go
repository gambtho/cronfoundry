package webapi

import (
	"context"
	"net/http"
	"time"
)

type ctxKey int

const sessionKey ctxKey = 0

// RequireSession is middleware that validates the cf_session cookie.
// Attaches SessionClaims to the request context on success; returns 401 otherwise.
func RequireSession(masterKey []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("cf_session")
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "authentication required", "unauthorized")
			return
		}
		claims, err := VerifySession(c.Value, masterKey)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid session", "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole wraps a handler requiring a valid session with the given role.
// "admin" role may access "viewer" routes; "viewer" may not access "admin" routes.
func RequireRole(masterKey []byte, role string, next http.Handler) http.Handler {
	return RequireSession(masterKey, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := SessionClaimsFromContext(r.Context())
		if role == "admin" && claims.Role != "admin" {
			writeErr(w, http.StatusForbidden, "admin role required", "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// SessionClaimsFromContext returns the claims attached by RequireSession.
// Returns zero value if the middleware was not applied.
func SessionClaimsFromContext(ctx context.Context) SessionClaims {
	c, _ := ctx.Value(sessionKey).(SessionClaims)
	return c
}

// mustClaims retrieves the session claims from the request context.
// Panics if called outside of RequireSession middleware (programmer error).
func mustClaims(r *http.Request) SessionClaims {
	claims, ok := r.Context().Value(sessionKey).(SessionClaims)
	if !ok {
		panic("webapi: mustClaims called outside RequireSession middleware")
	}
	return claims
}

// NewTestSessionCookie creates a signed session cookie for use in handler tests.
func NewTestSessionCookie(masterKey []byte, login, role string) (*http.Cookie, error) {
	value, err := SignSession(SessionClaims{Login: login, Role: role}, masterKey, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{Name: "cf_session", Value: value}, nil
}

