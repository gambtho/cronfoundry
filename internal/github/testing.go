package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// MustTestPrivateKey returns a newly-generated 2048-bit RSA key in PKCS#1 PEM
// form. Intended for tests only; the exported capital-M name lets other
// packages' test files call it.
func MustTestPrivateKey(t *testing.T) ([]byte, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("github: MustTestPrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	return pemBytes, &priv.PublicKey
}
