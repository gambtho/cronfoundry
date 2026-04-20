package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gambtho/cronfoundry/internal/token"
)

type ctxKey int

const claimsKey ctxKey = 0

// requireBearer is a middleware that extracts the Authorization header,
// verifies it via the signer, attaches the claims to the request context,
// and calls the next handler. Invalid tokens produce 401.
func requireBearer(signer *token.Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			claims, err := signer.Verify(strings.TrimPrefix(auth, "Bearer "))
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext returns the verified run claims attached by the
// requireBearer middleware. Zero value if the middleware wasn't applied.
func ClaimsFromContext(ctx context.Context) token.RunClaims {
	c, _ := ctx.Value(claimsKey).(token.RunClaims)
	return c
}
