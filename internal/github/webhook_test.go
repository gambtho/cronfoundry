package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyWebhookSignature_Valid(t *testing.T) {
	secret := []byte("topsecret")
	body := []byte(`{"hello":"world"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := VerifyWebhookSignature(secret, body, sig); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestVerifyWebhookSignature_Tampered(t *testing.T) {
	secret := []byte("topsecret")
	body := []byte(`{"hello":"world"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tampered := []byte(`{"hello":"mars"}`)
	if err := VerifyWebhookSignature(secret, tampered, sig); err == nil {
		t.Fatal("expected signature mismatch error")
	}
}

func TestVerifyWebhookSignature_MalformedHeader(t *testing.T) {
	for _, sig := range []string{"", "sha1=abcd", "sha256=notvalidhex", "sha256="} {
		if err := VerifyWebhookSignature([]byte("s"), []byte("b"), sig); err == nil {
			t.Fatalf("%q: expected error", sig)
		}
	}
}
