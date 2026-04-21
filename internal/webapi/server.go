package webapi

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secretstore"
)

// Deps holds everything webapi handlers need.
type Deps struct {
	MasterKey         []byte
	OAuthClientID     string
	OAuthClientSecret string
	AdminLogins       []string
	ViewerLogins      []string
	// GitHubAPIBase overrides the GitHub API base URL in tests. Empty = real GitHub.
	GitHubAPIBase string
	// Queries provides DB access for /api/* handlers.
	Queries *dbgen.Queries
	// Secrets provides secret store access for /api/secrets handlers.
	Secrets secretstore.SecretStore
	// APIBaseURL is the base URL for the internal API (used by run-now).
	APIBaseURL string
	// WebhookSecret is the shared HMAC secret registered with the GitHub App.
	// When empty, POST /webhook/github responds 503 Service Unavailable.
	WebhookSecret []byte
	// Syncer triggers a one-off repo sync. Injected from cmd/cronfoundry/serve.go
	// as a thin wrapper around sync.Poller.SyncOne.
	Syncer RepoSyncer
}

// resolveRole returns "admin", "viewer", or "" (not allowed). Queries the
// app_user table keyed by (org_id, github_login). Any error (including
// pgx.ErrNoRows) collapses to "" so OAuth callers see a uniform "not
// allowed" response without information leakage. Env-var allowlists are
// no longer consulted at runtime — they seed the table once on first
// startup (see cmd/cronfoundry/serve.go bootstrap block).
func (d Deps) resolveRole(ctx context.Context, orgID pgtype.UUID, login string) string {
	if d.Queries == nil {
		return ""
	}
	role, err := d.Queries.GetUserRole(ctx, dbgen.GetUserRoleParams{
		OrgID:       orgID,
		GithubLogin: login,
	})
	if err != nil {
		return ""
	}
	return role
}

// RegisterRoutes registers /oauth/*, /api/*, and /* (SPA catch-all) on mux.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	session := func(h http.Handler) http.Handler {
		return RequireSession(deps.MasterKey, h)
	}
	adminOnly := func(h http.Handler) http.Handler {
		return RequireRole(deps.MasterKey, "admin", h)
	}

	// P3a routes
	mux.Handle("GET /api/me", session(meHandler{}))
	oh := oauthHandlers{deps: deps}
	mux.HandleFunc("GET /oauth/login", oh.login)
	mux.HandleFunc("GET /oauth/callback", oh.callback)
	mux.HandleFunc("GET /oauth/logout", oh.logout)

	// Repos
	rh := &reposHandler{deps: deps}
	mux.Handle("GET /api/repos", session(http.HandlerFunc(rh.list)))
	mux.Handle("POST /api/repos", adminOnly(http.HandlerFunc(rh.connect)))
	mux.Handle("DELETE /api/repos/{id}", adminOnly(http.HandlerFunc(rh.disconnect)))

	// Skills
	sh := &skillsHandler{deps: deps}
	mux.Handle("GET /api/skills", session(http.HandlerFunc(sh.list)))

	// Schedules
	sch := &schedulesHandler{deps: deps}
	mux.Handle("GET /api/schedules", session(http.HandlerFunc(sch.list)))
	mux.Handle("POST /api/schedules/{id}/pause", adminOnly(http.HandlerFunc(sch.pause)))
	mux.Handle("POST /api/schedules/{id}/resume", adminOnly(http.HandlerFunc(sch.resume)))
	mux.Handle("POST /api/schedules/{id}/run-now", adminOnly(http.HandlerFunc(sch.runNow)))

	// Runs
	rnh := &runsHandler{deps: deps}
	mux.Handle("GET /api/runs", session(http.HandlerFunc(rnh.list)))
	mux.Handle("GET /api/runs/{id}", session(http.HandlerFunc(rnh.get)))

	// Events
	evh := &eventsHandler{deps: deps}
	mux.Handle("GET /api/runs/{id}/events", session(http.HandlerFunc(evh.list)))
	mux.Handle("GET /api/runs/{id}/events/stream", session(http.HandlerFunc(evh.stream)))

	// Secrets
	sech := &secretsHandler{deps: deps}
	mux.Handle("GET /api/secrets", session(http.HandlerFunc(sech.list)))
	mux.Handle("POST /api/secrets", adminOnly(http.HandlerFunc(sech.create)))
	mux.Handle("PUT /api/secrets/{name}/rotate", adminOnly(http.HandlerFunc(sech.rotate)))
	mux.Handle("DELETE /api/secrets/{name}", adminOnly(http.HandlerFunc(sech.delete)))

	// Audit log (admin-only, read-only)
	ah := &auditHandler{deps: deps}
	mux.Handle("GET /api/audit", adminOnly(http.HandlerFunc(ah.list)))

	// Users (admin-only)
	uh := &usersHandler{deps: deps}
	mux.Handle("GET /api/users", adminOnly(http.HandlerFunc(uh.list)))
	mux.Handle("POST /api/users", adminOnly(http.HandlerFunc(uh.create)))
	mux.Handle("PATCH /api/users/{login}", adminOnly(http.HandlerFunc(uh.updateRole)))
	mux.Handle("DELETE /api/users/{login}", adminOnly(http.HandlerFunc(uh.delete)))

	// Webhooks (unauthenticated; HMAC-verified)
	wh := &webhookHandler{deps: deps, secret: deps.WebhookSecret, syncer: deps.Syncer}
	mux.Handle("POST /webhook/github", wh)

	// SPA catch-all — must be last
	mux.Handle("/", staticHandler())
}
