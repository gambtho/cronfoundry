package webapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// CSRFConfig configures the CSRF middleware.
type CSRFConfig struct {
	// AllowedOrigin is the scheme+host of the trusted public origin
	// (e.g. "https://cronfoundry.example.com"). When empty, the
	// Origin/Referer check is skipped — intended for local dev only.
	AllowedOrigin string
}

// CSRF returns a middleware that enforces double-submit cookie + Origin check
// on all non-safe HTTP methods. GET, HEAD, and OPTIONS pass through unchanged
// (these are the IETF "safe methods"); every other method — including POST,
// PATCH, PUT, DELETE, and any unknown verb — must present a matching cf_csrf
// cookie and X-CSRF-Token header.
func CSRF(cfg CSRFConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(CSRFCookieName)
			if err != nil || cookie.Value == "" {
				csrfReject(w, r, "csrf cookie missing")
				return
			}
			header := r.Header.Get(CSRFHeaderName)
			if header == "" {
				csrfReject(w, r, "csrf header missing")
				return
			}
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
				csrfReject(w, r, "csrf mismatch")
				return
			}

			if cfg.AllowedOrigin != "" {
				if reason, ok := checkOrigin(r, cfg.AllowedOrigin); !ok {
					csrfReject(w, r, reason)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func checkOrigin(r *http.Request, allowed string) (string, bool) {
	want, ok := normalizeOrigin(allowed)
	if !ok {
		// Misconfigured AllowedOrigin — fail closed.
		return "origin mismatch", false
	}

	candidate := r.Header.Get("Origin")
	if candidate == "" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			return "origin missing", false
		}
		candidate = ref
	}
	got, ok := normalizeOrigin(candidate)
	if !ok {
		return "origin missing", false
	}
	if got != want {
		return "origin mismatch", false
	}
	return "", true
}

// normalizeOrigin returns scheme://host (host lowercased, default port stripped,
// userinfo discarded) or ok=false if the input cannot be parsed as an origin.
func normalizeOrigin(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	switch {
	case port == "" ||
		(u.Scheme == "http" && port == "80") ||
		(u.Scheme == "https" && port == "443"):
		return u.Scheme + "://" + host, true
	default:
		return u.Scheme + "://" + host + ":" + port, true
	}
}

func csrfReject(w http.ResponseWriter, r *http.Request, reason string) {
	slog.Info("csrf reject", "method", r.Method, "path", r.URL.Path, "reason", reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
}

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
