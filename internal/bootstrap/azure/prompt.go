// internal/bootstrap/azure/prompt.go
package azure

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompt reads bootstrap inputs interactively. Empty input keeps the
// suggested default.
func Prompt(_ context.Context, stdin io.Reader, stdout io.Writer) (Inputs, error) {
	r := bufio.NewReader(stdin)
	in := Inputs{}
	var err error

	if in.Env, err = ask(r, stdout, "env suffix", "prod"); err != nil {
		return in, err
	}
	if in.Region, err = ask(r, stdout, "Azure region", "swedencentral"); err != nil {
		return in, err
	}
	if in.ImageOwner, err = ask(r, stdout, "GHCR image owner", "gambtho"); err != nil {
		return in, err
	}
	if in.ImageTag, err = ask(r, stdout, "image tag", "latest"); err != nil {
		return in, err
	}
	if in.GithubAppID, err = ask(r, stdout, "GitHub App ID (numeric)", ""); err != nil {
		return in, err
	}
	if in.OAuthClientID, err = ask(r, stdout, "OAuth Client ID (Iv23li...)", ""); err != nil {
		return in, err
	}
	if in.OAuthClientSecret, err = ask(r, stdout, "OAuth Client Secret", ""); err != nil {
		return in, err
	}
	pemPath, err := ask(r, stdout, "Path to GitHub App .pem file", "")
	if err != nil {
		return in, err
	}
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return in, fmt.Errorf("read pem: %w", err)
	}
	in.PEMContents = string(pemBytes)
	if in.AdminLogins, err = ask(r, stdout, "Admin GitHub login(s) (comma-separated)", ""); err != nil {
		return in, err
	}
	pw, err := ask(r, stdout, "Postgres admin password (blank to generate)", "")
	if err != nil {
		return in, err
	}
	if pw == "" {
		pw, err = GeneratePostgresPassword()
		if err != nil {
			return in, err
		}
		fmt.Fprintf(stdout, "  generated postgres password: %s\n", pw)
	}
	in.PostgresPassword = pw
	return in, nil
}

func ask(r *bufio.Reader, w io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return def, nil
	}
	return line, nil
}
