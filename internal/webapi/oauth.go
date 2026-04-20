package webapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	githubAuthorizeURL   = "https://github.com/login/oauth/authorize"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	githubUserURL        = "https://api.github.com/user"
)

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
	if r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// Verify the state cookie is a valid, unexpired signed token.
	if _, err := VerifySession(stateCookie.Value, h.deps.MasterKey); err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	accessToken, err := h.exchangeCode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	login, err := h.fetchLogin(r.Context(), accessToken)
	if err != nil {
		http.Error(w, "user fetch failed", http.StatusBadGateway)
		return
	}
	role := h.deps.resolveRole(login)
	if role == "" {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", MaxAge: -1, Path: "/"})
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github token endpoint returned %d", resp.StatusCode)
	}
	defer resp.Body.Close()
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github user endpoint returned %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
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
