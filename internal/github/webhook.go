package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// VerifyWebhookSignature checks the GitHub-supplied X-Hub-Signature-256 header
// against an HMAC-SHA256 of body using secret. sig must be "sha256=<hex>".
func VerifyWebhookSignature(secret, body []byte, sig string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return errors.New("github: verifyWebhookSignature: missing sha256= prefix")
	}
	want, err := hex.DecodeString(sig[len(prefix):])
	if err != nil {
		return fmt.Errorf("github: verifyWebhookSignature: invalid hex signature: %w", err)
	}
	if len(want) == 0 {
		return errors.New("github: verifyWebhookSignature: empty signature")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	got := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return errors.New("github: verifyWebhookSignature: signature mismatch")
	}
	return nil
}

// PushPayload is the subset of the GitHub push event we care about.
type PushPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}
