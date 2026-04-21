package webapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

const (
	githubAuthorizeURL   = "https://github.com/login/oauth/authorize"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	githubUserURL        = "https://api.github.com/user"
)

// githubClient is used for all outbound GitHub API calls. The 10-second timeout
// prevents slow or hung GitHub endpoints from holding goroutines indefinitely.
var githubClient = &http.Client{Timeout: 10 * time.Second}

type oauthHandlers struct{ deps Deps }

func (h oauthHandlers) login(w http.ResponseWriter, r *http.Request) {
	state, err := SignOAuthState(h.deps.MasterKey, 10*time.Minute)
	if err != nil {
		http.Error(w, "state generation failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(r.Host),
	})
	params := url.Values{
		"client_id": {h.deps.OAuthClientID},
		"state":     {state},
		"scope":     {"read:user"},
	}
	http.Redirect(w, r, githubAuthorizeURL+"?"+params.Encode(), http.StatusFound)
}

func (h oauthHandlers) callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	// Clear state cookie immediately — it's single-use regardless of outcome.
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", MaxAge: -1, Path: "/"})
	if r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// Verify signature and expiry — the equality check above is necessary but not
	// sufficient; without this, any attacker-supplied consistent (state, cookie) pair
	// would pass. VerifySession is the actual integrity guard.
	if _, err := VerifySession(stateCookie.Value, h.deps.MasterKey); err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	accessToken, err := h.exchangeCode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		slog.Error("oauth: token exchange failed", "err", err)
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	login, err := h.fetchLogin(r.Context(), accessToken)
	if err != nil {
		slog.Error("oauth: user fetch failed", "err", err)
		http.Error(w, "user fetch failed", http.StatusBadGateway)
		return
	}

	// Load the single-tenant organization so we can look up this user's row.
	var orgID pgtype.UUID
	if h.deps.Queries != nil {
		org, err := h.deps.Queries.GetFirstOrganization(r.Context())
		if err != nil {
			slog.Error("oauth: load org failed", "err", err)
			http.Error(w, "org load failed", http.StatusInternalServerError)
			return
		}
		orgID = org.ID
	}

	role, err := h.deps.resolveRole(r.Context(), orgID, login)
	if err != nil {
		// Infra error (not "user not found") — surface as 500 so an
		// operator hitting a DB blip doesn't see a misleading 403.
		slog.Error("oauth: resolve role failed", "login", login, "err", err)
		http.Error(w, "role lookup failed", http.StatusInternalServerError)
		return
	}
	if role == "" {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	// Refresh last_login_at. The upsert preserves the existing role on
	// conflict — a user's role is stable across logins; admins change it
	// via /api/users or the env-var bootstrap on first startup.
	if h.deps.Queries != nil {
		if _, err := h.deps.Queries.UpsertUserOnLogin(r.Context(), dbgen.UpsertUserOnLoginParams{
			OrgID:       orgID,
			GithubLogin: login,
			Role:        role,
		}); err != nil {
			// Don't fail the login — the user's role was already resolved.
			slog.Warn("oauth: upsert user on login failed", "err", err)
		}
	}

	session, err := SignSession(SessionClaims{Login: login, Role: role}, h.deps.MasterKey, 24*time.Hour)
	if err != nil {
		http.Error(w, "session creation failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cf_session",
		Value:    session,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(r.Host),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h oauthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "cf_session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/oauth/login", http.StatusFound)
}

func (h oauthHandlers) exchangeCode(ctx context.Context, code string) (string, error) {
	base := githubAccessTokenURL
	if h.deps.GitHubAPIBase != "" {
		base = h.deps.GitHubAPIBase + "/login/oauth/access_token"
	}
	params := url.Values{
		"client_id":     {h.deps.OAuthClientID},
		"client_secret": {h.deps.OAuthClientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base, strings.NewReader(params.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := githubClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github token endpoint returned %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("github error: %s", result.Error)
	}
	return result.AccessToken, nil
}

func (h oauthHandlers) fetchLogin(ctx context.Context, accessToken string) (string, error) {
	base := githubUserURL
	if h.deps.GitHubAPIBase != "" {
		base = h.deps.GitHubAPIBase + "/user"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := githubClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github user endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read github user response: %w", err)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("decode user: %w", err)
	}
	return user.Login, nil
}

// SignOAuthState generates a signed random state token for CSRF protection.
func SignOAuthState(key []byte, ttl time.Duration) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	return SignSession(SessionClaims{Login: nonce, Role: "state"}, key, ttl)
}

func isLocalhost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host // no port present
	}
	// Strip brackets from IPv6 addresses like [::1]
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
