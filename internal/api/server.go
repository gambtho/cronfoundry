// Package api hosts the /internal HTTP surface used by runner subprocesses
// to fetch their context, request scoped secrets, stream events, and
// finalize. No human-facing endpoints live here — that's P3's job.
package api

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/token"
)

// Deps bundles everything a handler might need. Passed once at server
// construction; each handler type embeds a reference to it.
type Deps struct {
	Pool          *pgxpool.Pool
	Signer        *token.Signer
	Secrets       secretstore.SecretStore
	Installations *github.InstallationCache
}

// NewServer builds an *http.Server with all handlers registered under
// /internal/*. Bind the returned server to a localhost address (default
// in `cronfoundry serve` is 127.0.0.1:8080).
func NewServer(addr string, deps Deps) *http.Server {
	mux := http.NewServeMux()

	// Health check is unauthenticated.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// All /internal/runs/* and /internal/secrets and /internal/repos/*
	// routes require a valid per-run bearer token.
	auth := requireBearer(deps.Signer)

	mux.Handle("GET /internal/runs/{id}/context", auth(runContextHandler{deps}))
	mux.Handle("GET /internal/secrets", auth(secretsHandler{deps}))
	mux.Handle("GET /internal/repos/{id}/clone-url", auth(cloneURLHandler{deps}))
	mux.Handle("POST /internal/runs/{id}/events", auth(eventsHandler{deps}))
	mux.Handle("POST /internal/runs/{id}/finalize", auth(finalizeHandler{deps}))

	// Manual trigger is unauthenticated (CLI-local). P3 will gate behind UI
	// session auth.
	mux.Handle("POST /internal/schedules/{id}/run-now", runNowHandler{deps})

	return &http.Server{Addr: addr, Handler: mux}
}

// Handler types. Each gets a real ServeHTTP in tasks 7-12; for now they all
// return 501 Not Implemented so the server compiles and handles routing.
type runContextHandler struct{ deps Deps }
type secretsHandler struct{ deps Deps }
type cloneURLHandler struct{ deps Deps }
type eventsHandler struct{ deps Deps }
type finalizeHandler struct{ deps Deps }
type runNowHandler struct{ deps Deps }
