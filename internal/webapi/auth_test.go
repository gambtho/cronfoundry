package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func okHandler(w http.ResponseWriter, r *http.Request) {
	claims := webapi.SessionClaimsFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(claims.Login))
}

func TestRequireSession_ValidCookie(t *testing.T) {
	key := testKey()
	claims := webapi.SessionClaims{Login: "bob", Role: "viewer"}
	cookie, err := webapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	rr := httptest.NewRecorder()

	webapi.RequireSession(key, http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "bob", rr.Body.String())
}

func TestRequireSession_MissingCookie(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	webapi.RequireSession(testKey(), http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireSession_ExpiredCookie(t *testing.T) {
	key := testKey()
	claims := webapi.SessionClaims{Login: "bob", Role: "viewer"}
	cookie, err := webapi.SignSession(claims, key, -time.Second)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	rr := httptest.NewRecorder()

	webapi.RequireSession(key, http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireSession_TamperedCookie(t *testing.T) {
	key := testKey()
	claims := webapi.SessionClaims{Login: "bob", Role: "viewer"}
	cookie, err := webapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)
	b := []byte(cookie)
	b[len(b)-1] ^= 0xFF

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: string(b)})
	rr := httptest.NewRecorder()

	webapi.RequireSession(key, http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
