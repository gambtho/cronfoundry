// internal/bootstrap/azure/inputs.go
package azure

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Inputs is everything bootstrap needs to deploy.
type Inputs struct {
	Env               string // resource-name suffix, e.g. "prod"
	Region            string // azure region
	ImageOwner        string // ghcr owner, default "gambtho"
	ImageTag          string // ghcr tag, default "latest"
	GithubAppID       string
	OAuthClientID     string
	OAuthClientSecret string
	PEMContents       string // raw PEM contents (with BEGIN/END headers)
	AdminLogins       string // comma-separated github logins
	PostgresPassword  string
}

var envSuffixRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// Validate checks each field per the Bicep deployment's constraints.
func (in Inputs) Validate() error {
	if !envSuffixRe.MatchString(in.Env) {
		return fmt.Errorf("env %q invalid: must be 1-10 lowercase alphanumerics", in.Env)
	}
	if in.Region == "" {
		return errors.New("region required")
	}
	if in.GithubAppID == "" || in.OAuthClientID == "" || in.OAuthClientSecret == "" {
		return errors.New("github app id, oauth client id, oauth client secret all required")
	}
	if len(in.PEMContents) < 16 {
		return errors.New("PEM contents missing or implausibly short")
	}
	if in.AdminLogins == "" {
		return errors.New("at least one admin github login required")
	}
	if in.PostgresPassword == "" {
		return errors.New("postgres password required")
	}
	if strings.ContainsAny(in.PostgresPassword, "@:/%#?&=") {
		return errors.New("postgres password contains URL-special characters; alphanumerics only")
	}
	return nil
}

// GeneratePostgresPassword returns a 24-character alphanumeric password.
// Postgres ends up in a connection-string URL where @ : / etc. would need
// encoding, so we keep it simple.
func GeneratePostgresPassword() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 24
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}
