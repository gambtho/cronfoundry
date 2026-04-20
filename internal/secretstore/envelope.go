package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

const keyLen = 32 // bytes — AES-256

// newWrappedDEK generates a fresh 32-byte data-encryption key, encrypts it
// under the master key, and returns (plainDEK, wrappedDEK). The wrapped form
// is nonce-prefixed so it can be persisted as a single byte slot.
func newWrappedDEK(master []byte) (dek, wrapped []byte, err error) {
	if len(master) != keyLen {
		return nil, nil, fmt.Errorf("envelope: master key must be %d bytes, got %d", keyLen, len(master))
	}
	dek = make([]byte, keyLen)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, fmt.Errorf("envelope: generate DEK: %w", err)
	}
	wrapped, _, err = encryptWithNoncePrefixed(master, dek)
	if err != nil {
		return nil, nil, err
	}
	return dek, wrapped, nil
}

// unwrapDEK reverses newWrappedDEK.
func unwrapDEK(master, wrapped []byte) ([]byte, error) {
	if len(master) != keyLen {
		return nil, fmt.Errorf("envelope: master key must be %d bytes, got %d", keyLen, len(master))
	}
	return decryptNoncePrefixed(master, wrapped)
}

// encrypt encrypts plaintext under dek. nonce is returned separately so the
// caller can decide storage layout.
func encrypt(dek, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, nil, fmt.Errorf("envelope: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("envelope: new GCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("envelope: generate nonce: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// decrypt reverses encrypt.
func decrypt(dek, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("envelope: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("envelope: new GCM: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("envelope: nonce length %d, want %d", len(nonce), gcm.NonceSize())
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("envelope: open: %w", err)
	}
	return plaintext, nil
}

// encryptWithNoncePrefixed wraps encrypt by prepending the nonce to the
// ciphertext — convenient for single-value storage slots (e.g., dek_wrapped).
func encryptWithNoncePrefixed(key, plaintext []byte) (nonceAndCiphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("envelope: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("envelope: new GCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("envelope: generate nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nonce, nil
}

func decryptNoncePrefixed(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("envelope: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("envelope: new GCM: %w", err)
	}
	n := gcm.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("envelope: payload shorter than nonce")
	}
	nonce, ct := data[:n], data[n:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("envelope: open: %w", err)
	}
	return pt, nil
}

// encodeMasterKey returns base64 for the env var format.
func encodeMasterKey(key []byte) string { return base64.StdEncoding.EncodeToString(key) }

// parseMasterKey decodes a base64 env var and validates the length.
func parseMasterKey(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("envelope: decode master key: %w", err)
	}
	if len(raw) != keyLen {
		return nil, fmt.Errorf("envelope: master key must be %d bytes after base64 decode, got %d", keyLen, len(raw))
	}
	return raw, nil
}

// GenerateMasterKey returns a fresh base64-encoded 32-byte master key,
// suitable for CRONFOUNDRY_MASTER_KEY.
func GenerateMasterKey() (string, error) {
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("envelope: generate master key: %w", err)
	}
	return encodeMasterKey(key), nil
}

// ParseMasterKey decodes the base64 form used in CRONFOUNDRY_MASTER_KEY.
// Intended for CLI entry points; callers inside this package use the
// unexported parseMasterKey directly.
func ParseMasterKey(encoded string) ([]byte, error) {
	return parseMasterKey(encoded)
}
