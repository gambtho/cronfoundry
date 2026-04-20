// Package token mints and verifies per-run bearer JWTs. The signing key is
// derived from the process-wide master key via HKDF so it's distinct from
// any other HMAC keys we might derive in the future.
package token

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const hkdfInfo = "cronfoundry:run-jwt"

// RunClaims are the fields a per-run JWT carries.
type RunClaims struct {
	RunID      uuid.UUID
	OrgID      uuid.UUID
	SecretRefs []string
	ExpiresAt  time.Time
}

// Signer signs and verifies RunClaims tokens.
type Signer struct {
	key []byte
}

// New derives a 32-byte HMAC signing key from the master key via HKDF-SHA256.
// The master key must be 32 bytes (validated at the secretstore layer).
func New(master []byte) *Signer {
	h := hkdf.New(sha256.New, master, nil, []byte(hkdfInfo))
	key := make([]byte, 32)
	_, _ = h.Read(key) // hkdf.Reader never errors on a 32-byte read of SHA-256 output
	return &Signer{key: key}
}

// Sign returns (compact JWT, sha256-hex-hash) for the given claims.
// The hash is what the server stores in run.runner_token_hash so the
// plaintext bearer never needs to live in the database.
func (s *Signer) Sign(c RunClaims) (tok, hash string, err error) {
	claims := jwtv5.MapClaims{
		"run_id":      c.RunID.String(),
		"org_id":      c.OrgID.String(),
		"secret_refs": c.SecretRefs,
		"exp":         c.ExpiresAt.Unix(),
	}
	jwt := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims)
	signed, err := jwt.SignedString(s.key)
	if err != nil {
		return "", "", fmt.Errorf("token: sign: %w", err)
	}
	return signed, s.HashToken(signed), nil
}

// Verify parses + cryptographically verifies a bearer token, returning its
// claims or an error. Expired tokens produce an error whose message contains
// "expired".
func (s *Signer) Verify(bearer string) (RunClaims, error) {
	parsed, err := jwtv5.Parse(bearer, func(t *jwtv5.Token) (any, error) {
		return s.key, nil
	}, jwtv5.WithValidMethods([]string{"HS256"}))
	if err != nil {
		if errors.Is(err, jwtv5.ErrTokenExpired) {
			return RunClaims{}, fmt.Errorf("token: expired")
		}
		return RunClaims{}, fmt.Errorf("token: verify: %w", err)
	}
	claims, ok := parsed.Claims.(jwtv5.MapClaims)
	if !ok || !parsed.Valid {
		return RunClaims{}, fmt.Errorf("token: verify: invalid claims")
	}

	var out RunClaims
	runStr, _ := claims["run_id"].(string)
	orgStr, _ := claims["org_id"].(string)
	if out.RunID, err = uuid.Parse(runStr); err != nil {
		return RunClaims{}, fmt.Errorf("token: verify: run_id: %w", err)
	}
	if out.OrgID, err = uuid.Parse(orgStr); err != nil {
		return RunClaims{}, fmt.Errorf("token: verify: org_id: %w", err)
	}
	// Require an exp claim. jwtv5 enforces exp > now automatically when the
	// claim is present, but it accepts tokens with no exp at all. A
	// never-expiring bearer is worse than a short-lived one, so we refuse
	// to verify unless the claim is set.
	expF, ok := claims["exp"].(float64)
	if !ok {
		return RunClaims{}, fmt.Errorf("token: verify: missing exp claim")
	}
	out.ExpiresAt = time.Unix(int64(expF), 0)
	if raw, ok := claims["secret_refs"].([]any); ok {
		out.SecretRefs = make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out.SecretRefs = append(out.SecretRefs, s)
			}
		}
	}
	return out, nil
}

// HashToken returns the hex-encoded sha256 of a bearer token. Deterministic
// across calls; stable for the same input.
func (s *Signer) HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
