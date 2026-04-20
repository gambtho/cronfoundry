// Package githubtest provides test helpers for code that uses internal/github.
// Kept in its own package so callers don't pull `testing` into production
// builds of internal/github.
package githubtest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// MustPrivateKey returns a newly-generated 2048-bit RSA key in PKCS#1 PEM
// form.
func MustPrivateKey(t *testing.T) ([]byte, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("githubtest: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	return pemBytes, &priv.PublicKey
}
