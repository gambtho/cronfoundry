package webapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func requestWithSession(t *testing.T, login, role string) *http.Request {
	t.Helper()
	key := testKey()
	claims := webapi.SessionClaims{Login: login, Role: role}
	cookie, err := webapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	return req
}

func TestMe_Admin(t *testing.T) {
	key := testKey()
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         key,
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
	})

	req := requestWithSession(t, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "alice", got["login"])
	assert.Equal(t, "admin", got["role"])
}

func TestMe_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         testKey(),
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
	})

	req := httptest.NewRequest("GET", "/api/me", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
