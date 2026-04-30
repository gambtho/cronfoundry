package server

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func randomKey(t *testing.T, n int) []byte {
	t.Helper()
	k := make([]byte, n)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestEnvelope_EncryptDecrypt_RoundTrip(t *testing.T) {
	master := randomKey(t, 32)
	plaintext := "sk-abc-123"

	dek, dekWrapped, err := newWrappedDEK(master)
	require.NoError(t, err)
	assert.Len(t, dek, 32)

	ciphertext, nonce, err := encrypt(dek, []byte(plaintext))
	require.NoError(t, err)
	assert.NotEqual(t, []byte(plaintext), ciphertext)

	// Separate recovery path: unwrap DEK, then decrypt.
	recoveredDEK, err := unwrapDEK(master, dekWrapped)
	require.NoError(t, err)
	assert.Equal(t, dek, recoveredDEK)

	recovered, err := decrypt(recoveredDEK, ciphertext, nonce)
	require.NoError(t, err)
	assert.Equal(t, plaintext, string(recovered))
}

func TestEnvelope_WrongMasterFailsUnwrap(t *testing.T) {
	master := randomKey(t, 32)
	wrong := randomKey(t, 32)
	_, wrapped, err := newWrappedDEK(master)
	require.NoError(t, err)

	_, err = unwrapDEK(wrong, wrapped)
	require.Error(t, err)
}

func TestEnvelope_MasterKeyLenMustBe32(t *testing.T) {
	short := make([]byte, 16)
	_, _, err := newWrappedDEK(short)
	require.Error(t, err)
}

func TestEnvelope_ParseMasterKey_Base64(t *testing.T) {
	master := randomKey(t, 32)
	encoded := encodeMasterKey(master)

	decoded, err := parseMasterKey(encoded)
	require.NoError(t, err)
	assert.Equal(t, master, decoded)
}

func TestEnvelope_ParseMasterKey_RejectsShort(t *testing.T) {
	short := randomKey(t, 16)
	_, err := parseMasterKey(encodeMasterKey(short))
	require.Error(t, err)
}

func TestEnvelope_GenerateMasterKey_DecodesTo32Bytes(t *testing.T) {
	encoded, err := GenerateMasterKey()
	require.NoError(t, err)
	decoded, err := parseMasterKey(encoded)
	require.NoError(t, err)
	assert.Len(t, decoded, 32)
}

func TestEnvelope_EachEncryptHasFreshNonce(t *testing.T) {
	// Two encrypts of the same plaintext under the same key must produce
	// different (ciphertext, nonce) pairs (AES-GCM requires unique nonces).
	dek := randomKey(t, 32)
	ct1, n1, err := encrypt(dek, []byte("same"))
	require.NoError(t, err)
	ct2, n2, err := encrypt(dek, []byte("same"))
	require.NoError(t, err)
	assert.NotEqual(t, n1, n2)
	assert.NotEqual(t, ct1, ct2)
}
