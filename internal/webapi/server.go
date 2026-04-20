package webapi

import "net/http"

// Deps holds everything webapi handlers need.
type Deps struct {
	MasterKey         []byte
	OAuthClientID     string
	OAuthClientSecret string
	AdminLogins       []string
	ViewerLogins      []string
	// GitHubAPIBase overrides the GitHub API base URL in tests. Empty = real GitHub.
	GitHubAPIBase string
}

// resolveRole returns "admin", "viewer", or "" (not allowed).
func (d Deps) resolveRole(login string) string {
	for _, l := range d.AdminLogins {
		if l == login {
			return "admin"
		}
	}
	for _, l := range d.ViewerLogins {
		if l == login {
			return "viewer"
		}
	}
	return ""
}

// RegisterRoutes registers /oauth/* and /api/* on mux.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	session := func(h http.Handler) http.Handler {
		return RequireSession(deps.MasterKey, h)
	}

	mux.Handle("GET /api/me", session(meHandler{}))

	// OAuth routes registered by oauthHandlers — implemented in oauth.go (Task 4).
	oh := oauthHandlers{deps: deps}
	mux.HandleFunc("GET /oauth/login", oh.login)
	mux.HandleFunc("GET /oauth/callback", oh.callback)
	mux.HandleFunc("GET /oauth/logout", oh.logout)
}

// oauthHandlers placeholder — full implementation in oauth.go (Task 4).
type oauthHandlers struct{ deps Deps }

func (h oauthHandlers) login(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
func (h oauthHandlers) callback(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
func (h oauthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
