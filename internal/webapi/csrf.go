package webapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// CSRFCookieName is the cookie that carries the per-session CSRF token.
// It is non-HttpOnly so the SPA can read it via document.cookie and echo it
// in X-CSRF-Token. The cookie's presence on the cronfoundry origin is the
// security primitive — an attacker on a foreign origin cannot read it.
const CSRFCookieName = "cf_csrf"

// CSRFHeaderName is the header the SPA must set on every state-changing
// request. The middleware compares its value to the cookie with a
// constant-time compare.
const CSRFHeaderName = "X-CSRF-Token"

// NewCSRFToken returns a 32-byte random token, base64url-encoded without
// padding (43 chars). Suitable for use as a per-session CSRF token.
func NewCSRFToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// SetCSRFCookie writes the cf_csrf cookie. Called from the OAuth callback
// alongside SetCookie for cf_session. HttpOnly is intentionally false.
func SetCSRFCookie(w http.ResponseWriter, token string, maxAge int, host string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(host),
	})
}

// ClearCSRFCookie deletes the cf_csrf cookie. Called from logout.
func ClearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CSRFCookieName, MaxAge: -1, Path: "/"})
}
