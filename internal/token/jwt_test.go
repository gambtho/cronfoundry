package token

import (
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/hkdf"
)

func randomMaster(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	signer := New(randomMaster(t))
	runID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	tok, hash, err := signer.Sign(RunClaims{
		RunID:      runID,
		OrgID:      orgID,
		SecretRefs: []string{"slack_webhook", "openai_key"},
		ExpiresAt:  time.Now().Add(10 * time.Minute),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.NotEmpty(t, hash)
	assert.Len(t, hash, 64, "hash should be hex-encoded sha256 (64 chars)")

	claims, err := signer.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, runID, claims.RunID)
	assert.Equal(t, orgID, claims.OrgID)
	assert.ElementsMatch(t, []string{"slack_webhook", "openai_key"}, claims.SecretRefs)
}

func TestVerify_RejectsExpired(t *testing.T) {
	signer := New(randomMaster(t))
	tok, _, err := signer.Sign(RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(-1 * time.Second),
	})
	require.NoError(t, err)
	_, err = signer.Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerify_RejectsDifferentKey(t *testing.T) {
	a := New(randomMaster(t))
	b := New(randomMaster(t))

	tok, _, err := a.Sign(RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	_, err = b.Verify(tok)
	require.Error(t, err)
}

func TestHashToken_IsStable(t *testing.T) {
	signer := New(randomMaster(t))
	assert.Equal(t, signer.HashToken("same"), signer.HashToken("same"))
	assert.NotEqual(t, signer.HashToken("a"), signer.HashToken("b"))
}

func TestHashToken_IsSha256Hex(t *testing.T) {
	signer := New(randomMaster(t))
	hash := signer.HashToken("anything")
	assert.Len(t, hash, 64)
	// sha256("anything") = hex ee0874170b7f6f32b8c2ac9573c428d35b575270a66b757c2c0185d2bd09718d
	assert.Equal(t, "ee0874170b7f6f32b8c2ac9573c428d35b575270a66b757c2c0185d2bd09718d", hash)
}

// TestVerify_RejectsMissingExp confirms that a token without an exp claim
// fails verification — jwtv5's default validator accepts tokens without
// exp, but we require exp to defend against accidentally issuing a
// never-expiring bearer.
func TestVerify_RejectsMissingExp(t *testing.T) {
	master := randomMaster(t)
	signer := New(master)

	// Reconstruct the internal HKDF key so we can hand-sign a token with
	// no exp claim without going through Sign (which always adds exp).
	h := hkdf.New(sha256.New, master, nil, []byte(hkdfInfo))
	key := make([]byte, 32)
	_, _ = h.Read(key)

	claims := jwtv5.MapClaims{
		"run_id": uuid.New().String(),
		"org_id": uuid.New().String(),
		// Deliberately omit "exp".
	}
	jwt := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims)
	tok, err := jwt.SignedString(key)
	require.NoError(t, err)

	_, err = signer.Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exp")
}
