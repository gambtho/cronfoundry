package webapi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func testKey() []byte { return []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }

func TestSession_RoundTrip(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), 24*time.Hour)
	require.NoError(t, err)
	got, err := webapi.VerifySession(cookie, testKey())
	require.NoError(t, err)
	assert.Equal(t, "alice", got.Login)
	assert.Equal(t, "admin", got.Role)
}

func TestSession_Expired(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), -1*time.Second)
	require.NoError(t, err)
	_, err = webapi.VerifySession(cookie, testKey())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestSession_TamperedSignature(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), 24*time.Hour)
	require.NoError(t, err)
	b := []byte(cookie)
	b[len(b)-1] ^= 0xFF
	_, err = webapi.VerifySession(string(b), testKey())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestSession_TamperedPayload(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), 24*time.Hour)
	require.NoError(t, err)
	b := []byte(cookie)
	b[0] ^= 0xFF
	_, err = webapi.VerifySession(string(b), testKey())
	require.Error(t, err)
}

func TestSession_WrongKey(t *testing.T) {
	cookie, err := webapi.SignSession(webapi.SessionClaims{Login: "alice", Role: "admin"}, testKey(), 24*time.Hour)
	require.NoError(t, err)
	_, err = webapi.VerifySession(cookie, []byte("different-key-here-xxxxxxxxxxxxxxx"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}
