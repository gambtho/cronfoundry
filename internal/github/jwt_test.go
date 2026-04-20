package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestPEM produces a PKCS#1 RSA key in PEM form suitable for AppJWT's
// private-key argument.
func generateTestPEM(t *testing.T) (privPEM []byte, pub *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	return privPEM, &priv.PublicKey
}

func TestAppJWT_Signs_And_IncludesExpectedClaims(t *testing.T) {
	privPEM, pub := generateTestPEM(t)

	token, err := AppJWT("12345", privPEM)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	parsed, err := jwtv5.Parse(token, func(t *jwtv5.Token) (interface{}, error) {
		return pub, nil
	}, jwtv5.WithValidMethods([]string{"RS256"}))
	require.NoError(t, err)
	require.True(t, parsed.Valid)

	claims := parsed.Claims.(jwtv5.MapClaims)
	assert.Equal(t, "12345", claims["iss"])

	iat, ok := claims["iat"].(float64)
	require.True(t, ok)
	exp, ok := claims["exp"].(float64)
	require.True(t, ok)
	// exp should be 9 minutes beyond iat (GitHub cap is 10 minutes; we leave
	// a 1-minute cushion).
	assert.InDelta(t, 9*60, exp-iat, 5)

	// iat should be slightly in the past (clock-skew tolerance).
	now := float64(time.Now().Unix())
	assert.Less(t, iat, now+1)
	assert.Greater(t, iat, now-120)
}

func TestAppJWT_RejectsMalformedPEM(t *testing.T) {
	_, err := AppJWT("12345", []byte("not a pem"))
	require.Error(t, err)
}

func TestAppJWT_RejectsNonRSAKey(t *testing.T) {
	// A valid PEM block whose body isn't a valid key blob.
	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})
	_, err := AppJWT("12345", bad)
	require.Error(t, err)
}

func TestAppJWT_AcceptsPKCS8(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	token, err := AppJWT("12345", pemBytes)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}
