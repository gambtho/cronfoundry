package webapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
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
	// RateLimit configures per-IP rate limiting on public routes.
	RateLimit RateLimiterConfig
}

// resolveRole returns ("admin"|"viewer", nil) for allowed logins, ("", nil)
// for a missing row (not allowed), or ("", err) for infrastructure errors.
// Separating the two negative cases lets callers map "missing row" to 403
// and "infra error" to 500 — a transient DB blip must NOT silently lock
// legitimate admins out with a misleading "access denied". Env-var
// allowlists are no longer consulted at runtime — they seed the table
// once on first startup (see cmd/cronfoundry/serve.go bootstrap block).
func (d Deps) resolveRole(ctx context.Context, orgID pgtype.UUID, login string) (string, error) {
	if d.Queries == nil {
		return "", nil
	}
	role, err := d.Queries.GetUserRole(ctx, dbgen.GetUserRoleParams{
		OrgID:       orgID,
		GithubLogin: login,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

// RegisterRoutes registers /oauth/*, /api/*, and /* (SPA catch-all) on mux.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	rl, err := NewRateLimiter(deps.RateLimit)
	if err != nil {
		panic(fmt.Sprintf("webapi: rate limiter init: %v", err))
	}
	session := func(h http.Handler) http.Handler {
		return rl.Group("api", RequireSession(deps.MasterKey, h))
	}
	adminOnly := func(h http.Handler) http.Handler {
		return rl.Group("api", RequireRole(deps.MasterKey, "admin", h))
	}

	// P3a routes
	mux.Handle("GET /api/me", session(meHandler{}))
	oh := oauthHandlers{deps: deps}
	mux.Handle("GET /oauth/login", rl.Group("oauth", http.HandlerFunc(oh.login)))
	mux.Handle("GET /oauth/callback", rl.Group("oauth", http.HandlerFunc(oh.callback)))
	mux.HandleFunc("GET /oauth/logout", oh.logout) // intentionally not rate limited

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
	mux.Handle("PATCH /api/schedules/{id}/overrides", adminOnly(http.HandlerFunc(sch.patchOverrides)))
	mux.Handle("DELETE /api/schedules/{id}/overrides", adminOnly(http.HandlerFunc(sch.deleteOverrides)))

	// Runs
	rnh := &runsHandler{deps: deps}
	mux.Handle("GET /api/runs", session(http.HandlerFunc(rnh.list)))
	mux.Handle("GET /api/runs/{id}", session(http.HandlerFunc(rnh.get)))

	// Events
	evh := &eventsHandler{deps: deps}
	mux.Handle("GET /api/runs/{id}/events", session(http.HandlerFunc(evh.list)))
	mux.Handle("GET /api/runs/{id}/events/stream", session(rl.SSE(http.HandlerFunc(evh.stream))))

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
	mux.Handle("POST /webhook/github", rl.Group("webhook", wh))

	// Copilot Enterprise device flow (admin-only)
	cph := &copilotConnectHandler{deps: deps}
	mux.Handle("POST /api/copilot/connect", adminOnly(http.HandlerFunc(cph.startFlow)))
	mux.Handle("GET /api/copilot/connect/{device_code}/poll", adminOnly(http.HandlerFunc(cph.poll)))

	// Internal: copilot token endpoint (called by runner)
	cpth := &copilotTokenHandler{deps: deps}
	mux.Handle("GET /internal/runs/{id}/copilot-token", http.HandlerFunc(cpth.get))

	// SPA catch-all — must be last
	mux.Handle("/", staticHandler())
}
