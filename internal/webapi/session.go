// Package webapi hosts the /api/* and /oauth/* browser-facing HTTP surface.
// Runner-facing endpoints live in internal/api.
package webapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SessionClaims is the payload embedded in the cf_session cookie.
type SessionClaims struct {
	Login string `json:"login"`
	Role  string `json:"role"`
	Exp   int64  `json:"exp"` // Unix seconds
}

// SignSession encodes claims as a signed cookie value:
//
//	base64url(JSON) + "." + base64url(HMAC-SHA256(payload, key))
func SignSession(claims SessionClaims, key []byte, ttl time.Duration) (string, error) {
	if len(key) == 0 {
		return "", errors.New("session: signing key must not be empty")
	}
	claims.Exp = time.Now().Add(ttl).Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	sig := sign([]byte(enc), key)
	return enc + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifySession parses and validates a signed cookie value produced by SignSession.
func VerifySession(cookie string, key []byte) (SessionClaims, error) {
	if len(key) == 0 {
		return SessionClaims{}, errors.New("session: signing key must not be empty")
	}
	dot := lastDot(cookie)
	if dot < 0 {
		return SessionClaims{}, errors.New("invalid signature: malformed cookie")
	}
	enc, sigEnc := cookie[:dot], cookie[dot+1:]

	sigGot, err := base64.RawURLEncoding.DecodeString(sigEnc)
	if err != nil {
		return SessionClaims{}, errors.New("invalid signature: bad encoding")
	}
	sigExpected := sign([]byte(enc), key)
	if !hmac.Equal(sigGot, sigExpected) {
		return SessionClaims{}, errors.New("invalid signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return SessionClaims{}, fmt.Errorf("invalid session: %w", err)
	}
	var claims SessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return SessionClaims{}, fmt.Errorf("invalid session: %w", err)
	}
	if time.Now().Unix() > claims.Exp {
		return SessionClaims{}, errors.New("session expired")
	}
	return claims, nil
}

func sign(data, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
