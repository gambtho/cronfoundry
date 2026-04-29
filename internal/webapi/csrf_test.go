package webapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
