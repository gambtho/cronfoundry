package api

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/token"
)

func randomMaster(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

// helloHandler echoes the run_id from the claims so we can verify the
// middleware attached them correctly.
func helloHandler(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	_, _ = w.Write([]byte(claims.RunID.String()))
}

func TestRequireBearer_Accepts(t *testing.T) {
	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	h := requireBearer(signer, nil)(http.HandlerFunc(helloHandler))

	req := httptest.NewRequest("GET", "/internal/runs/anything", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", rr.Body.String())
}

func TestRequireBearer_Rejects_NoHeader(t *testing.T) {
	signer := token.New(randomMaster(t))
	h := requireBearer(signer, nil)(http.HandlerFunc(helloHandler))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/internal/runs/x", nil))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearer_Rejects_NotBearerScheme(t *testing.T) {
	signer := token.New(randomMaster(t))
	h := requireBearer(signer, nil)(http.HandlerFunc(helloHandler))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/internal/runs/x", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearer_Rejects_BadToken(t *testing.T) {
	signer := token.New(randomMaster(t))
	h := requireBearer(signer, nil)(http.HandlerFunc(helloHandler))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/internal/runs/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearer_Rejects_WrongKey(t *testing.T) {
	a := token.New(randomMaster(t))
	b := token.New(randomMaster(t))

	tok, _, err := a.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	// Verified by `b` (different key) — must reject.
	h := requireBearer(b, nil)(http.HandlerFunc(helloHandler))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/internal/runs/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearerOrAPIKey_AcceptsAPIKey(t *testing.T) {
	called := false
	handler := requireBearerOrAPIKey(nil, nil, "super-secret-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/runs/1/context", nil)
	req.Header.Set("X-Runner-Key", "super-secret-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, called)
}

func TestRequireBearerOrAPIKey_RejectsWrongAPIKey(t *testing.T) {
	handler := requireBearerOrAPIKey(nil, nil, "super-secret-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/runs/1/context", nil)
	req.Header.Set("X-Runner-Key", "wrong-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearerOrAPIKey_RejectsNoCredentials(t *testing.T) {
	handler := requireBearerOrAPIKey(nil, nil, "")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/runs/1/context", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNewServer_RoutesRegistered(t *testing.T) {
	signer := token.New(randomMaster(t))
	deps := Deps{Signer: signer}
	srv := NewServer("127.0.0.1:0", deps)
	require.NotNil(t, srv)

	// Healthz is unauthenticated.
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "ok", rr.Body.String())

	// Protected route without bearer → 401.
	rr2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr2, httptest.NewRequest(
		"GET", "/internal/runs/00000000-0000-0000-0000-000000000001/context", nil))
	assert.Equal(t, http.StatusUnauthorized, rr2.Code)

	// Protected route with bearer whose run_id doesn't match the URL →
	// runContextHandler returns 403 Forbidden (the URL-vs-claim guard fires
	// before any DB work).
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	req := httptest.NewRequest(
		"GET", "/internal/runs/00000000-0000-0000-0000-000000000001/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr3 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr3, req)
	assert.Equal(t, http.StatusForbidden, rr3.Code)

	// Manual trigger endpoint is unauthenticated — no 401 is produced. With a
	// bad-format UUID the handler rejects with 400 (not 401), confirming the
	// route is reachable without a bearer token.
	//
	// httptest.NewRequest defaults RemoteAddr to 192.0.2.1:1234 (non-loopback),
	// which the MAJ-9 loopback guard rejects before the handler's UUID parse
	// runs. Force RemoteAddr to a real loopback IP so we reach the 400 path.
	rr4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(
		"POST", "/internal/schedules/anything/run-now", nil)
	req4.RemoteAddr = "127.0.0.1:1234"
	srv.Handler.ServeHTTP(rr4, req4)
	assert.Equal(t, http.StatusBadRequest, rr4.Code)
}
