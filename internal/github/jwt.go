// Package github wraps the GitHub App authentication + REST API calls
// CronFoundry needs for repo sync and writeback. Higher-level orchestration
// (the sync loop, dispatcher) lives in internal/sync and internal/scheduler.
package github

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// AppJWT mints a short-lived (9-minute) JWT signed with the GitHub App's
// private key. Use this token as the Authorization bearer when calling
// GitHub App-level endpoints — specifically to exchange for per-installation
// access tokens.
//
// The returned JWT includes:
//   - iss = appID (numeric App identifier, passed as a string)
//   - iat = now - 60s (clock-skew tolerance)
//   - exp = iat + 9min (≈ now + 8min; GitHub caps at 10min so this leaves a 2min cushion)
//
// Accepts PKCS#1 ("RSA PRIVATE KEY") or PKCS#8 ("PRIVATE KEY") PEM blocks.
func AppJWT(appID string, privateKeyPEM []byte) (string, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return "", fmt.Errorf("github: appJWT: parse PEM: no PEM block")
	}
	var priv *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		p, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("github: appJWT: parse PKCS1: %w", err)
		}
		priv = p
	case "PRIVATE KEY":
		keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("github: appJWT: parse PKCS8: %w", err)
		}
		p, ok := keyAny.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("github: appJWT: PKCS8 key is not RSA")
		}
		priv = p
	default:
		return "", fmt.Errorf("github: appJWT: unexpected PEM type %q", block.Type)
	}

	now := time.Now()
	iat := now.Add(-60 * time.Second)
	claims := jwtv5.MapClaims{
		"iss": appID,
		"iat": iat.Unix(),
		"exp": iat.Add(9 * time.Minute).Unix(),
	}
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		return "", fmt.Errorf("github: appJWT: sign: %w", err)
	}
	return signed, nil
}
