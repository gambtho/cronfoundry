package webapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCSRFToken_LengthAndAlphabet(t *testing.T) {
	tok, err := NewCSRFToken()
	require.NoError(t, err)
	// 32 bytes -> 43 chars unpadded base64url
	assert.Len(t, tok, 43)
	assert.False(t, strings.ContainsAny(tok, "+/="), "must be base64url unpadded")
}

func TestNewCSRFToken_UniquePerCall(t *testing.T) {
	a, err := NewCSRFToken()
	require.NoError(t, err)
	b, err := NewCSRFToken()
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}

func TestSetCSRFCookie_Localhost_NoSecure(t *testing.T) {
	w := httptest.NewRecorder()
	SetCSRFCookie(w, "tok", 3600, "localhost:8080")
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, CSRFCookieName, c.Name)
	assert.Equal(t, "tok", c.Value)
	assert.Equal(t, "/", c.Path)
	assert.Equal(t, 3600, c.MaxAge)
	assert.False(t, c.HttpOnly, "MUST be readable by SPA — do not change to true")
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.False(t, c.Secure, "localhost should not require Secure")
}

func TestSetCSRFCookie_NonLocalhost_Secure(t *testing.T) {
	w := httptest.NewRecorder()
	SetCSRFCookie(w, "tok", 3600, "cronfoundry.example.com")
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.True(t, c.Secure, "non-localhost MUST set Secure")
	assert.False(t, c.HttpOnly, "MUST stay non-HttpOnly")
}

func TestClearCSRFCookie(t *testing.T) {
	w := httptest.NewRecorder()
	ClearCSRFCookie(w)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, CSRFCookieName, c.Name)
	assert.Less(t, c.MaxAge, 0)
	assert.Equal(t, "/", c.Path)
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestCSRF_GETPassesThrough(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_NoCookie_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf cookie missing")
}

func TestCSRF_POST_CookieNoHeader_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf header missing")
}

func TestCSRF_POST_Mismatch_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "xyz")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf mismatch")
}

func TestCSRF_POST_Match_NoOriginRequired_OK(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_OriginMatch_OK(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	req.Header.Set("Origin", "https://cronfoundry.example.com")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_OriginMismatch_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "origin mismatch")
}

func TestCSRF_POST_RefererFallback_OK(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	req.Header.Set("Referer", "https://cronfoundry.example.com/runs/abc")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_NoOriginNoReferer_AllowedOriginSet_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "origin missing")
}

func TestCSRF_HEADAndOPTIONS_PassThrough(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	for _, method := range []string{"HEAD", "OPTIONS"} {
		req := httptest.NewRequest(method, "/api/runs", nil)
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, method)
	}
}

func TestCSRF_AllUnsafeMethods_Enforced(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	for _, method := range []string{"PATCH", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			// No cookie/header → 403
			req := httptest.NewRequest(method, "/api/anything", nil)
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "csrf cookie missing")

			// Matching cookie+header → OK
			req = httptest.NewRequest(method, "/api/anything", nil)
			req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
			req.Header.Set(CSRFHeaderName, "tok")
			w = httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestCSRF_OriginNormalization(t *testing.T) {
	cases := []struct {
		name    string
		allowed string
		origin  string
		want    int
	}{
		{"default port matches omitted (https)", "https://cf.example.com", "https://cf.example.com:443", http.StatusOK},
		{"default port matches omitted (http)", "http://cf.example.com", "http://cf.example.com:80", http.StatusOK},
		{"non-default port mismatches", "https://cf.example.com", "https://cf.example.com:8443", http.StatusForbidden},
		{"userinfo stripped", "https://cf.example.com", "https://user:pass@cf.example.com", http.StatusOK},
		{"hostname case-insensitive", "https://cf.example.com", "https://CF.Example.COM", http.StatusOK},
		{"scheme mismatch", "https://cf.example.com", "http://cf.example.com", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw := CSRF(CSRFConfig{AllowedOrigin: tc.allowed})(okHandler())
			req := httptest.NewRequest("POST", "/x", nil)
			req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
			req.Header.Set(CSRFHeaderName, "tok")
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}

func TestRegisterRoutes_AdminMutationsRequireCSRF(t *testing.T) {
	mux := http.NewServeMux()
	deps := Deps{
		MasterKey:     bytes.Repeat([]byte("k"), 32),
		PublicBaseURL: "",
	}
	RegisterRoutes(mux, deps)

	// POST /api/repos with a valid admin session but NO CSRF cookie/header
	// should be rejected by CSRF middleware (403), not auth (401).
	sess, err := SignSession(SessionClaims{Login: "admin", Role: "admin"}, deps.MasterKey, time.Hour)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: sess})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf cookie missing")
}
