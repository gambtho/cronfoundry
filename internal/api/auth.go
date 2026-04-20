package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/token"
)

type ctxKey int

const claimsKey ctxKey = 0

// requireBearer is a middleware that extracts the Authorization header,
// cryptographically verifies it via the signer, and — when a pool is
// supplied — also validates that the token's SHA-256 hash matches the
// run.runner_token_hash column. The hash check defends against replay:
// even a signature-valid token is rejected once the run has been rotated
// to a new hash, or if the token was never the one issued for this run.
//
// Pool may be nil in auth-only unit tests that don't stand up a database.
// In that case only the JWT signature check runs. Production callers
// ("cronfoundry serve") always construct Deps with a live Pool so the
// hash check is enforced.
func requireBearer(signer *token.Signer, pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			raw := strings.TrimPrefix(auth, "Bearer ")
			claims, err := signer.Verify(raw)
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			if pool != nil {
				q := dbgen.New(pool)
				run, err := q.GetRun(r.Context(),
					pgtype.UUID{Bytes: claims.RunID, Valid: true})
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						http.Error(w, "unknown run", http.StatusUnauthorized)
						return
					}
					http.Error(w, "run lookup: "+err.Error(), http.StatusInternalServerError)
					return
				}
				if run.RunnerTokenHash != signer.HashToken(raw) {
					http.Error(w, "token hash mismatch", http.StatusUnauthorized)
					return
				}
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
